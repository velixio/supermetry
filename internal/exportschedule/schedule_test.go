package exportschedule

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/platform/crypto"
	"supermetry/internal/platform/mail"
	"supermetry/internal/privacy"
	"supermetry/internal/siteexport"
	"supermetry/internal/testpg"
)

type call struct {
	site     int32
	from, to string
}

type recordingExporter struct {
	mu    sync.Mutex
	calls []call
	err   error
}

func (e *recordingExporter) Write(_ context.Context, w io.Writer, siteID int32, from, to time.Time) error {
	e.mu.Lock()
	e.calls = append(e.calls, call{siteID, from.Format(time.DateOnly), to.Format(time.DateOnly)})
	e.mu.Unlock()
	if e.err != nil {
		return e.err
	}
	_, err := fmt.Fprintf(w, "zip:%d:%s:%s", siteID, from.Format(time.DateOnly), to.Format(time.DateOnly))
	return err
}

type fakeMail struct {
	mu   sync.Mutex
	sent []mail.Message
}

func (m *fakeMail) Send(_ context.Context, msg mail.Message) error {
	m.mu.Lock()
	m.sent = append(m.sent, msg)
	m.mu.Unlock()
	return nil
}
func (m *fakeMail) Enabled() bool { return true }

type rig struct {
	pool *pgxpool.Pool
	svc  *Service
	exp  *recordingExporter
	s3   *fakeS3
	mail *fakeMail
	now  time.Time
	ctx  context.Context
}

func newRig(t *testing.T, exporter func(*pgxpool.Pool) Exporter) *rig {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		insert into orgs(id,name,slug) overriding system value values (1,'Org','org'),(2,'Other','other');
		insert into users(id,email,name,password_hash) overriding system value values (11,'owner@example.test','Owner','hash'),(12,'viewer@example.test','Viewer','hash');
		insert into memberships(org_id,user_id,role) values (1,11,'owner'),(1,12,'viewer');
		insert into sites(id,org_id,public_id,name,timezone,retention_class,created_at) overriding system value values
		 (7,1,'exportsch001','One','UTC',30,'2026-10-01T08:00:00Z'),
		 (8,1,'exportsch002','Two','UTC',30,'2026-10-05T08:00:00Z'),
		 (9,2,'exportsch003','Foreign','UTC',30,'2026-10-01T08:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	box, err := crypto.NewSecretBox(bytes.Repeat([]byte("k"), 32), "test/exports")
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{pool: pool, exp: &recordingExporter{}, s3: newFakeS3(t), mail: &fakeMail{}, now: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC), ctx: ctx}
	var e Exporter = r.exp
	if exporter != nil {
		e = exporter(pool)
	}
	r.svc = New(pool, box, e, Options{Mailer: r.mail, AllowPrivate: true, Now: func() time.Time { return r.now },
		S3: S3Options{TLSConfig: r.s3.tlsConfig(), PartSize: 5 << 20}})
	return r
}

func (r *rig) input(site *int32) Input {
	return Input{Name: "Nightly", SiteID: site, Frequency: Daily, Incremental: true, Endpoint: r.s3.Server.URL, Region: "us-east-1",
		Bucket: "bucket", Prefix: "/backups/", AccessKeyID: "AKIA", Secret: "super-secret", PathStyle: true}
}

func (r *rig) runNow(t *testing.T, id int64) {
	t.Helper()
	if err := r.svc.RunNow(r.ctx, 1, id); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.RunDue(r.ctx); err != nil {
		t.Fatal(err)
	}
}

func i32(v int32) *int32 { return &v }

func TestNextRun(t *testing.T) {
	at := func(s string) time.Time { v, _ := time.Parse(time.RFC3339, s); return v }
	for _, c := range []struct{ freq, from, want string }{
		{Daily, "2026-10-10T12:00:00Z", "2026-10-11T03:00:00Z"},
		{Daily, "2026-10-10T01:00:00Z", "2026-10-10T03:00:00Z"},
		{Weekly, "2026-10-10T12:00:00Z", "2026-10-12T03:00:00Z"}, // Saturday -> Monday
		{Weekly, "2026-10-12T04:00:00Z", "2026-10-19T03:00:00Z"},
		{Weekly, "2026-10-12T01:00:00Z", "2026-10-12T03:00:00Z"},
		{Monthly, "2026-10-10T12:00:00Z", "2026-11-01T03:00:00Z"},
		{Monthly, "2026-12-31T23:00:00Z", "2027-01-01T03:00:00Z"},
	} {
		if got := NextRun(c.freq, at(c.from)); !got.Equal(at(c.want)) {
			t.Errorf("NextRun(%s, %s) = %s, want %s", c.freq, c.from, got.Format(time.RFC3339), c.want)
		}
	}
}

func TestCreateValidatesAndEncryptsSecret(t *testing.T) {
	r := newRig(t, nil)
	s, err := r.svc.Create(r.ctx, 1, 11, r.input(i32(7)))
	if err != nil {
		t.Fatal(err)
	}
	if !s.SecretSet || s.Prefix != "backups" || !s.NextRunAt.Equal(time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("schedule = %+v", s)
	}
	if raw, _ := json.Marshal(s); bytes.Contains(raw, []byte("super-secret")) {
		t.Fatal("secret leaked in JSON")
	}
	var sealed []byte
	if err := r.pool.QueryRow(r.ctx, `select secret_enc from export_schedules where id=$1`, s.ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("super-secret")) || len(sealed) < 12 {
		t.Fatal("secret is not encrypted at rest")
	}
	bad := r.input(i32(9)) // another organization's site
	if _, err := r.svc.Create(r.ctx, 1, 11, bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("foreign site error = %v", err)
	}
	bad = r.input(nil)
	bad.Frequency = "hourly"
	if _, err := r.svc.Create(r.ctx, 1, 11, bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("frequency error = %v", err)
	}
	if _, err := r.svc.Get(r.ctx, 2, s.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org Get = %v", err)
	}
	// Update keeps the secret unless a new one is given.
	name := "Renamed"
	if _, err := r.svc.Update(r.ctx, 1, s.ID, Patch{Name: &name}); err != nil {
		t.Fatal(err)
	}
	dest, _, err := r.svc.destination(r.ctx, s.ID)
	if err != nil || dest.Secret != "super-secret" {
		t.Fatalf("secret after update = %q, %v", dest.Secret, err)
	}
}

func TestCreateRejectsPrivateEndpointWithoutOptIn(t *testing.T) {
	r := newRig(t, nil)
	strict := New(r.pool, r.svc.box, r.exp, Options{Now: r.svc.now})
	if _, err := strict.Create(r.ctx, 1, 11, r.input(nil)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Create with a 127.0.0.1 endpoint = %v, want ErrInvalid", err)
	}
}

func TestIncrementalExportOnlyNewDays(t *testing.T) {
	r := newRig(t, nil)
	s, err := r.svc.Create(r.ctx, 1, 11, r.input(i32(7)))
	if err != nil {
		t.Fatal(err)
	}
	r.runNow(t, s.ID)
	r.now = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	r.runNow(t, s.ID)
	r.runNow(t, s.ID) // nothing new since the last run
	want := []call{{7, "2026-10-01", "2026-10-09"}, {7, "2026-10-10", "2026-10-11"}}
	if !slices.Equal(r.exp.calls, want) {
		t.Fatalf("export calls = %v, want %v", r.exp.calls, want)
	}
	if b, ok := r.s3.object("bucket/backups/site-7/supermetry-site-7-2026-10-10-2026-10-11.zip"); !ok || string(b) != "zip:7:2026-10-10:2026-10-11" {
		t.Fatalf("second object = %q, %v (have %v)", b, ok, r.s3.keysStored())
	}
	runs, err := r.svc.Runs(r.ctx, 1, s.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	var statuses []string
	for _, run := range runs {
		statuses = append(statuses, run.Status)
	}
	if !slices.Equal(statuses, []string{"skipped", "succeeded", "succeeded"}) || runs[1].Bytes == 0 {
		t.Fatalf("run history = %v %+v", statuses, runs)
	}
	got, _ := r.svc.Get(r.ctx, 1, s.ID)
	if got.LastStatus == nil || *got.LastStatus != "skipped" || !got.NextRunAt.Equal(time.Date(2026, 10, 13, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("schedule after runs = %+v", got)
	}
}

func TestFullModeAndOrgScope(t *testing.T) {
	r := newRig(t, nil)
	in := r.input(nil)
	in.Incremental = false
	s, _ := r.svc.Create(r.ctx, 1, 11, in)
	r.runNow(t, s.ID)
	r.now = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	r.runNow(t, s.ID)
	want := []call{{7, "2026-10-01", "2026-10-09"}, {8, "2026-10-05", "2026-10-09"}, {7, "2026-10-01", "2026-10-11"}, {8, "2026-10-05", "2026-10-11"}}
	if !slices.Equal(r.exp.calls, want) {
		t.Fatalf("calls = %v, want %v (site 9 belongs to another org)", r.exp.calls, want)
	}
}

func TestFailureIsRecordedRetriedAndNotifiedOnce(t *testing.T) {
	r := newRig(t, nil)
	s, _ := r.svc.Create(r.ctx, 1, 11, r.input(i32(7)))
	r.s3.FailPut = true
	r.runNow(t, s.ID)
	got, _ := r.svc.Get(r.ctx, 1, s.ID)
	if got.LastStatus == nil || *got.LastStatus != "failed" || got.LastError == "" || !got.NextRunAt.Equal(r.now.Add(retryAfterFailure)) {
		t.Fatalf("after failure: %+v", got)
	}
	if len(r.mail.sent) != 1 || r.mail.sent[0].To != "owner@example.test" {
		t.Fatalf("notifications = %+v (only owners and admins are told)", r.mail.sent)
	}
	var progress int
	_ = r.pool.QueryRow(r.ctx, `select count(*) from export_schedule_progress`).Scan(&progress)
	if progress != 0 {
		t.Fatal("a failed run must not advance the incremental watermark")
	}
	r.now = r.now.Add(7 * time.Hour)
	r.runNow(t, s.ID)
	if len(r.mail.sent) != 1 {
		t.Fatalf("a repeated failure mailed again: %d", len(r.mail.sent))
	}
	r.s3.FailPut = false
	r.now = r.now.Add(7 * time.Hour)
	r.runNow(t, s.ID)
	got, _ = r.svc.Get(r.ctx, 1, s.ID)
	if *got.LastStatus != "succeeded" || got.LastError != "" {
		t.Fatalf("after recovery: %+v", got)
	}
}

func TestExporterFailureAbortsUploadAndRecordsError(t *testing.T) {
	r := newRig(t, nil)
	r.exp.err = errors.New("anonymous history")
	s, _ := r.svc.Create(r.ctx, 1, 11, r.input(i32(7)))
	r.runNow(t, s.ID)
	got, _ := r.svc.Get(r.ctx, 1, s.ID)
	if *got.LastStatus != "failed" || got.LastError == "" {
		t.Fatalf("schedule = %+v", got)
	}
	if len(r.s3.keysStored()) != 0 {
		t.Fatalf("objects stored after exporter failure: %v", r.s3.keysStored())
	}
}

func TestTestEndpoint(t *testing.T) {
	r := newRig(t, nil)
	s, _ := r.svc.Create(r.ctx, 1, 11, r.input(nil))
	if err := r.svc.Test(r.ctx, 1, s.ID); err != nil {
		t.Fatal(err)
	}
	r.s3.FailPut = true
	if err := r.svc.Test(r.ctx, 1, s.ID); err == nil {
		t.Fatal("want failure")
	}
}

// End to end with the real exporter: the uploaded object is a ZIP with a manifest.
func TestUploadsRealExportWithManifest(t *testing.T) {
	r := newRig(t, func(pool *pgxpool.Pool) Exporter {
		e := siteexport.New(pool)
		e.SetTrackingMode(privacy.Identified)
		return e
	})
	if _, err := r.pool.Exec(r.ctx, `
		create table rollup_day_sched_test partition of rollup_day for values from ('2026-10-01') to ('2026-10-20');
		insert into rollup_day(site_id,day,visitors,visits,pageviews,bounces,duration_ms,engagement_ms,scroll_sum,scroll_n)
		values (7,'2026-10-02',3,4,8,1,12000,0,0,0)`); err != nil {
		t.Fatal(err)
	}
	s, _ := r.svc.Create(r.ctx, 1, 11, r.input(i32(7)))
	r.runNow(t, s.ID)
	body, ok := r.s3.object("bucket/backups/site-7/supermetry-site-7-2026-10-01-2026-10-09.zip")
	if !ok {
		t.Fatalf("no export uploaded: %v", r.s3.keysStored())
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	if last := zr.File[len(zr.File)-1].Name; last != "manifest.json" {
		t.Fatalf("last entry %s", last)
	}
}
