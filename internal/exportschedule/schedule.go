package exportschedule

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/platform/crypto"
	"supermetry/internal/platform/mail"
	"supermetry/internal/scheduler"
)

var (
	ErrNotFound = errors.New("exportschedule: not found")
	ErrInvalid  = errors.New("exportschedule: invalid argument")
)

// Frequencies a schedule can run at (03:00 UTC on the given period start).
const (
	Daily   = "daily"
	Weekly  = "weekly"
	Monthly = "monthly"
)

// Exporter writes one site export ZIP. *siteexport.Exporter satisfies it.
type Exporter interface {
	Write(ctx context.Context, w io.Writer, siteID int32, from, to time.Time) error
}

// Schedule is a stored schedule. The secret is never exposed.
type Schedule struct {
	ID          int64      `json:"id"`
	OrgID       int64      `json:"org_id"`
	SiteID      *int32     `json:"site_id"`
	Name        string     `json:"name"`
	Frequency   string     `json:"frequency"`
	Incremental bool       `json:"incremental"`
	Enabled     bool       `json:"enabled"`
	Endpoint    string     `json:"endpoint"`
	Region      string     `json:"region"`
	Bucket      string     `json:"bucket"`
	Prefix      string     `json:"prefix"`
	AccessKeyID string     `json:"access_key_id"`
	SecretSet   bool       `json:"secret_set"`
	PathStyle   bool       `json:"path_style"`
	NextRunAt   time.Time  `json:"next_run_at"`
	LastRunAt   *time.Time `json:"last_run_at"`
	LastStatus  *string    `json:"last_status"`
	LastError   string     `json:"last_error"`
	CreatedAt   time.Time  `json:"created_at"`
}

// Input creates a schedule. SiteID nil exports every site of the organization.
type Input struct {
	Name        string
	SiteID      *int32
	Frequency   string
	Incremental bool
	Endpoint    string
	Region      string
	Bucket      string
	Prefix      string
	AccessKeyID string
	Secret      string
	PathStyle   bool
}

// Patch changes a schedule; nil fields are kept. An empty Secret keeps the stored one.
type Patch struct {
	Name        *string
	Frequency   *string
	Incremental *bool
	Enabled     *bool
	Endpoint    *string
	Region      *string
	Bucket      *string
	Prefix      *string
	AccessKeyID *string
	Secret      *string
	PathStyle   *bool
}

// Run is one site export attempt.
type Run struct {
	ID         int64      `json:"id"`
	ScheduleID int64      `json:"schedule_id"`
	SiteID     *int32     `json:"site_id"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Status     string     `json:"status"`
	FromDay    *string    `json:"from_day"`
	ToDay      *string    `json:"to_day"`
	Bytes      int64      `json:"bytes"`
	ObjectKey  string     `json:"object_key"`
	Error      string     `json:"error"`
}

// Service stores schedules and runs them.
type Service struct {
	pool         *pgxpool.Pool
	box          *crypto.SecretBox
	exporter     Exporter
	mailer       mail.Sender
	baseURL      string
	allowPrivate bool
	now          func() time.Time
	newS3        func(Destination) (*S3, error)
	log          *slog.Logger
}

// Options configure a Service.
type Options struct {
	Mailer       mail.Sender // optional: failure notices to org owners and admins
	BaseURL      string
	AllowPrivate bool
	Now          func() time.Time
	S3           S3Options // overrides for tests
}

func New(pool *pgxpool.Pool, box *crypto.SecretBox, exporter Exporter, opt Options) *Service {
	s := &Service{pool: pool, box: box, exporter: exporter, mailer: opt.Mailer, baseURL: opt.BaseURL,
		allowPrivate: opt.AllowPrivate, now: opt.Now, log: slog.Default()}
	if s.now == nil {
		s.now = time.Now
	}
	s3opt := opt.S3
	s3opt.AllowPrivate = opt.AllowPrivate
	s.newS3 = func(d Destination) (*S3, error) { return NewS3(d, s3opt) }
	return s
}

// NextRun returns the first run time after t: 03:00 UTC on the next day, the
// next Monday, or the first of next month.
func NextRun(frequency string, t time.Time) time.Time {
	t = t.UTC()
	day := time.Date(t.Year(), t.Month(), t.Day(), 3, 0, 0, 0, time.UTC)
	switch frequency {
	case Weekly:
		day = day.AddDate(0, 0, (8-int(day.Weekday()))%7)
		if day.Weekday() == time.Monday && !day.After(t) {
			day = day.AddDate(0, 0, 7)
		}
		if !day.After(t) {
			day = day.AddDate(0, 0, 7)
		}
		return day
	case Monthly:
		first := time.Date(t.Year(), t.Month(), 1, 3, 0, 0, 0, time.UTC)
		if !first.After(t) {
			first = first.AddDate(0, 1, 0)
		}
		return first
	default:
		if !day.After(t) {
			day = day.AddDate(0, 0, 1)
		}
		return day
	}
}

const scheduleColumns = `id,org_id,site_id,name,frequency,incremental,enabled,endpoint,region,bucket,prefix,access_key_id,
 path_style,next_run_at,last_run_at,last_status,last_error,created_at`

func scan(row pgx.Row) (Schedule, error) {
	var s Schedule
	err := row.Scan(&s.ID, &s.OrgID, &s.SiteID, &s.Name, &s.Frequency, &s.Incremental, &s.Enabled, &s.Endpoint, &s.Region,
		&s.Bucket, &s.Prefix, &s.AccessKeyID, &s.PathStyle, &s.NextRunAt, &s.LastRunAt, &s.LastStatus, &s.LastError, &s.CreatedAt)
	s.SecretSet = true
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

func validFrequency(f string) bool { return f == Daily || f == Weekly || f == Monthly }

func (s *Service) checkInput(name, frequency string, d Destination) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 80 {
		return fmt.Errorf("%w: name must be 1 to 80 characters", ErrInvalid)
	}
	if !validFrequency(frequency) {
		return fmt.Errorf("%w: frequency must be daily, weekly or monthly", ErrInvalid)
	}
	if _, err := d.validate(s.allowPrivate); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return nil
}

// Create stores a schedule; the first run is at the next window.
func (s *Service) Create(ctx context.Context, orgID, userID int64, in Input) (Schedule, error) {
	d := Destination{Endpoint: strings.TrimSpace(in.Endpoint), Region: strings.TrimSpace(in.Region), Bucket: strings.TrimSpace(in.Bucket),
		Prefix: strings.Trim(strings.TrimSpace(in.Prefix), "/"), AccessKeyID: strings.TrimSpace(in.AccessKeyID), Secret: in.Secret, PathStyle: in.PathStyle}
	if err := s.checkInput(in.Name, in.Frequency, d); err != nil {
		return Schedule{}, err
	}
	if in.SiteID != nil {
		var one int
		err := s.pool.QueryRow(ctx, `select 1 from sites where id=$1 and org_id=$2 and deleted_at is null`, *in.SiteID, orgID).Scan(&one)
		if errors.Is(err, pgx.ErrNoRows) {
			return Schedule{}, fmt.Errorf("%w: site not found in this organization", ErrInvalid)
		}
		if err != nil {
			return Schedule{}, err
		}
	}
	var uid any
	if userID != 0 {
		uid = userID
	}
	return scan(s.pool.QueryRow(ctx, `
insert into export_schedules(org_id,site_id,name,frequency,incremental,endpoint,region,bucket,prefix,access_key_id,secret_enc,path_style,next_run_at,created_by)
values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) returning `+scheduleColumns,
		orgID, in.SiteID, strings.TrimSpace(in.Name), in.Frequency, in.Incremental, d.Endpoint, d.Region, d.Bucket, d.Prefix, d.AccessKeyID,
		s.box.Seal([]byte(d.Secret)), d.PathStyle, NextRun(in.Frequency, s.now()), uid))
}

func (s *Service) List(ctx context.Context, orgID int64) ([]Schedule, error) {
	rows, err := s.pool.Query(ctx, `select `+scheduleColumns+` from export_schedules where org_id=$1 order by id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Service) Get(ctx context.Context, orgID, id int64) (Schedule, error) {
	return scan(s.pool.QueryRow(ctx, `select `+scheduleColumns+` from export_schedules where id=$1 and org_id=$2`, id, orgID))
}

func (s *Service) Update(ctx context.Context, orgID, id int64, p Patch) (Schedule, error) {
	cur, err := s.Get(ctx, orgID, id)
	if err != nil {
		return Schedule{}, err
	}
	if p.Name != nil {
		cur.Name = strings.TrimSpace(*p.Name)
	}
	if p.Frequency != nil {
		cur.Frequency = *p.Frequency
	}
	if p.Incremental != nil {
		cur.Incremental = *p.Incremental
	}
	if p.Enabled != nil {
		cur.Enabled = *p.Enabled
	}
	if p.Endpoint != nil {
		cur.Endpoint = strings.TrimSpace(*p.Endpoint)
	}
	if p.Region != nil {
		cur.Region = strings.TrimSpace(*p.Region)
	}
	if p.Bucket != nil {
		cur.Bucket = strings.TrimSpace(*p.Bucket)
	}
	if p.Prefix != nil {
		cur.Prefix = strings.Trim(strings.TrimSpace(*p.Prefix), "/")
	}
	if p.AccessKeyID != nil {
		cur.AccessKeyID = strings.TrimSpace(*p.AccessKeyID)
	}
	if p.PathStyle != nil {
		cur.PathStyle = *p.PathStyle
	}
	check := Destination{Endpoint: cur.Endpoint, Region: cur.Region, Bucket: cur.Bucket, AccessKeyID: cur.AccessKeyID, Secret: "x"}
	if err := s.checkInput(cur.Name, cur.Frequency, check); err != nil {
		return Schedule{}, err
	}
	next := cur.NextRunAt
	if p.Frequency != nil {
		next = NextRun(cur.Frequency, s.now())
	}
	var secret []byte
	if p.Secret != nil && *p.Secret != "" {
		secret = s.box.Seal([]byte(*p.Secret))
	}
	return scan(s.pool.QueryRow(ctx, `
update export_schedules set name=$3,frequency=$4,incremental=$5,enabled=$6,endpoint=$7,region=$8,bucket=$9,prefix=$10,
 access_key_id=$11,path_style=$12,next_run_at=$13,secret_enc=coalesce($14,secret_enc),updated_at=now()
where id=$1 and org_id=$2 returning `+scheduleColumns,
		id, orgID, cur.Name, cur.Frequency, cur.Incremental, cur.Enabled, cur.Endpoint, cur.Region, cur.Bucket, cur.Prefix,
		cur.AccessKeyID, cur.PathStyle, next, secret))
}

func (s *Service) Delete(ctx context.Context, orgID, id int64) error {
	tag, err := s.pool.Exec(ctx, `delete from export_schedules where id=$1 and org_id=$2`, id, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RunNow makes the schedule due; the scheduler picks it up within a minute.
func (s *Service) RunNow(ctx context.Context, orgID, id int64) error {
	tag, err := s.pool.Exec(ctx, `update export_schedules set next_run_at=$3 where id=$1 and org_id=$2 and enabled`, id, orgID, s.now())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Runs lists the latest run attempts of a schedule, newest first.
func (s *Service) Runs(ctx context.Context, orgID, id int64, limit int) ([]Run, error) {
	if _, err := s.Get(ctx, orgID, id); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
select id,schedule_id,site_id,started_at,finished_at,status,from_day::text,to_day::text,bytes,object_key,error
from export_runs where schedule_id=$1 order by id desc limit $2`, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.ScheduleID, &r.SiteID, &r.StartedAt, &r.FinishedAt, &r.Status, &r.FromDay, &r.ToDay, &r.Bytes, &r.ObjectKey, &r.Error); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Service) destination(ctx context.Context, id int64) (Destination, Schedule, error) {
	var sealed []byte
	sc, err := scan(s.pool.QueryRow(ctx, `select `+scheduleColumns+` from export_schedules where id=$1`, id))
	if err != nil {
		return Destination{}, sc, err
	}
	if err := s.pool.QueryRow(ctx, `select secret_enc from export_schedules where id=$1`, id).Scan(&sealed); err != nil {
		return Destination{}, sc, err
	}
	secret, err := s.box.Open(sealed)
	if err != nil {
		return Destination{}, sc, fmt.Errorf("decrypt export secret: %w", err)
	}
	return Destination{Endpoint: sc.Endpoint, Region: sc.Region, Bucket: sc.Bucket, Prefix: sc.Prefix,
		AccessKeyID: sc.AccessKeyID, Secret: string(secret), PathStyle: sc.PathStyle}, sc, nil
}

// Test checks the stored destination by writing and deleting a small object.
func (s *Service) Test(ctx context.Context, orgID, id int64) error {
	if _, err := s.Get(ctx, orgID, id); err != nil {
		return err
	}
	dest, _, err := s.destination(ctx, id)
	if err != nil {
		return err
	}
	client, err := s.newS3(dest)
	if err != nil {
		return err
	}
	return client.Check(ctx)
}

// Job runs due schedules once a minute.
func (s *Service) Job() scheduler.Job {
	return scheduler.Job{Name: "export-schedules", Every: time.Minute, Run: s.RunDue}
}

// RunDue runs every enabled schedule whose time has come, one after another.
func (s *Service) RunDue(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `update export_runs set status='failed',error='interrupted',finished_at=now()
		where status='running' and started_at < now()-interval '6 hours'`); err != nil {
		return err
	}
	rows, err := s.pool.Query(ctx, `select id from export_schedules where enabled and next_run_at <= $1 order by next_run_at limit 20`, s.now())
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.runSchedule(ctx, id)
	}
	return nil
}
