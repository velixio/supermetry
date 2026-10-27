package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/platform/store"
)

const (
	// KindTelegram is the only channel kind so far.
	KindTelegram = "telegram"

	// DefaultMinInterval is the minimum gap between two sends of the same
	// event kind to one org.
	DefaultMinInterval = 10 * time.Minute
	// MaxConsecutiveFailures auto-disables a channel.
	MaxConsecutiveFailures = 10
	// TestInterval is the minimum gap between test messages for one org.
	TestInterval = 30 * time.Second

	logRetention   = 30 * 24 * time.Hour
	deliverTimeout = 30 * time.Second
	maxInFlight    = 16
)

var (
	ErrInvalid   = errors.New("invalid notification channel")
	ErrNotFound  = errors.New("notification channel not found")
	ErrForbidden = errors.New("the principal cannot manage notification channels")
	ErrTooSoon   = errors.New("a test message was sent less than 30 seconds ago")
)

// DeliveryError is a failed test delivery; Message is already scrubbed.
type DeliveryError struct{ Message string }

func (e *DeliveryError) Error() string { return e.Message }

// Channel is the API view of a stored channel. The bot token is never part of
// it; TokenSet only says whether one is stored.
type Channel struct {
	ID                  int64         `json:"id"`
	Kind                string        `json:"kind"`
	Enabled             bool          `json:"enabled"`
	ChatID              string        `json:"chat_id"`
	TokenSet            bool          `json:"token_set"`
	Events              map[Kind]bool `json:"events"`
	CreatedAt           time.Time     `json:"created_at"`
	UpdatedAt           time.Time     `json:"updated_at"`
	LastOKAt            *time.Time    `json:"last_ok_at"`
	LastError           string        `json:"last_error"`
	ConsecutiveFailures int           `json:"consecutive_failures"`
}

// TelegramInput is a save request. A nil or empty Token keeps the stored one
// and is only allowed when a channel already exists.
type TelegramInput struct {
	Token   *string
	ChatID  string
	Enabled bool
	Events  map[Kind]bool
}

// Options configures a Service.
type Options struct {
	Sender      Sender
	Now         func() time.Time
	MinInterval time.Duration
}

// Service stores channels and implements Notifier.
type Service struct {
	pool        *pgxpool.Pool
	store       *store.Store
	box         *platformcrypto.SecretBox
	sender      Sender
	now         func() time.Time
	minInterval time.Duration

	wg       sync.WaitGroup
	inFlight chan struct{}

	mu       sync.Mutex
	lastTest map[int64]time.Time
}

// NewService builds a Service. box seals bot tokens; sender defaults to the
// production Telegram client.
func NewService(pool *pgxpool.Pool, box *platformcrypto.SecretBox, options Options) (*Service, error) {
	sender := options.Sender
	if sender == nil {
		client, err := NewTelegramClient(TelegramOptions{})
		if err != nil {
			return nil, err
		}
		sender = client
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	interval := options.MinInterval
	if interval <= 0 {
		interval = DefaultMinInterval
	}
	return &Service{
		pool: pool, store: &store.Store{Pool: pool}, box: box, sender: sender, now: now, minInterval: interval,
		inFlight: make(chan struct{}, maxInFlight), lastTest: map[int64]time.Time{},
	}, nil
}

const channelColumns = `id,kind,enabled,chat_id,events,created_at,updated_at,last_ok_at,last_error,consecutive_failures`

func scanChannel(row pgx.Row) (Channel, error) {
	var c Channel
	var raw []byte
	if err := row.Scan(&c.ID, &c.Kind, &c.Enabled, &c.ChatID, &raw, &c.CreatedAt, &c.UpdatedAt, &c.LastOKAt, &c.LastError, &c.ConsecutiveFailures); err != nil {
		return Channel{}, err
	}
	c.TokenSet = true // config_enc is NOT NULL
	c.Events = normalizeEvents(raw)
	return c, nil
}

// normalizeEvents returns a map with every registered kind present.
func normalizeEvents(raw []byte) map[Kind]bool {
	stored := map[string]bool{}
	_ = json.Unmarshal(raw, &stored)
	out := make(map[Kind]bool, len(registry))
	for _, info := range registry {
		out[info.Kind] = stored[string(info.Kind)]
	}
	return out
}

// List returns the org's channels.
func (s *Service) List(ctx context.Context, orgID int64) ([]Channel, error) {
	rows, err := s.pool.Query(ctx, `select `+channelColumns+` from org_notification_channels where org_id=$1 order by id`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list notification channels: %w", err)
	}
	defer rows.Close()
	channels := []Channel{}
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, fmt.Errorf("scan notification channel: %w", err)
		}
		channels = append(channels, c)
	}
	return channels, rows.Err()
}

func validateInput(in TelegramInput) (map[string]bool, error) {
	chat := strings.TrimSpace(in.ChatID)
	if !ValidChatID(chat) {
		return nil, fmt.Errorf("%w: chat_id must be a numeric chat id or an @channelname", ErrInvalid)
	}
	events := map[string]bool{}
	for kind, on := range in.Events {
		if !Valid(kind) {
			return nil, fmt.Errorf("%w: unknown event kind %q", ErrInvalid, truncate(string(kind), 40))
		}
		events[string(kind)] = on
	}
	return events, nil
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// SaveTelegram creates or updates the org's Telegram channel and audits it.
func (s *Service) SaveTelegram(ctx context.Context, principal authz.Principal, in TelegramInput) (Channel, error) {
	if !authz.Can(principal, authz.ActionChannelsManage, 0) {
		return Channel{}, ErrForbidden
	}
	events, err := validateInput(in)
	if err != nil {
		return Channel{}, err
	}
	chat := strings.TrimSpace(in.ChatID)
	token := ""
	if in.Token != nil {
		token = strings.TrimSpace(*in.Token)
	}
	if token != "" && !ValidToken(token) {
		return Channel{}, fmt.Errorf("%w: the bot token does not look like a Telegram token", ErrInvalid)
	}
	eventsJSON, _ := json.Marshal(events)
	var saved Channel
	err = s.store.Tx(ctx, func(tx pgx.Tx) error {
		var id int64
		var wasEnabled bool
		lookup := tx.QueryRow(ctx, `select id,enabled from org_notification_channels where org_id=$1 and kind='telegram' for update`, principal.OrgID)
		switch err := lookup.Scan(&id, &wasEnabled); {
		case errors.Is(err, pgx.ErrNoRows):
			if token == "" {
				return fmt.Errorf("%w: a bot token is required", ErrInvalid)
			}
			row := tx.QueryRow(ctx, `insert into org_notification_channels(org_id,kind,enabled,config_enc,chat_id,events)
				values ($1,'telegram',$2,$3,$4,$5) returning `+channelColumns,
				principal.OrgID, in.Enabled, s.box.Seal([]byte(token)), chat, eventsJSON)
			saved, err = scanChannel(row)
			if err != nil {
				return fmt.Errorf("create notification channel: %w", err)
			}
		case err != nil:
			return fmt.Errorf("find notification channel: %w", err)
		default:
			// A new token or re-enabling a channel clears the failure state.
			reset := token != "" || (in.Enabled && !wasEnabled)
			var sealed []byte
			if token != "" {
				sealed = s.box.Seal([]byte(token))
			}
			row := tx.QueryRow(ctx, `update org_notification_channels set
					enabled=$2, chat_id=$3, events=$4, updated_at=now(),
					config_enc=coalesce($5,config_enc),
					consecutive_failures=case when $6 then 0 else consecutive_failures end,
					last_error=case when $6 then '' else last_error end
				where id=$1 returning `+channelColumns, id, in.Enabled, chat, eventsJSON, sealed, reset)
			saved, err = scanChannel(row)
			if err != nil {
				return fmt.Errorf("update notification channel: %w", err)
			}
		}
		return audit.Write(ctx, tx, principal, "org_notification_channel.saved", "org_notification_channel", saved.ID, map[string]any{
			"kind": KindTelegram, "enabled": saved.Enabled, "token_changed": token != "", "events": enabledKinds(saved.Events),
		})
	})
	return saved, err
}

func enabledKinds(events map[Kind]bool) []string {
	out := []string{}
	for _, info := range registry {
		if events[info.Kind] {
			out = append(out, string(info.Kind))
		}
	}
	return out
}

// DeleteTelegram removes the org's Telegram channel.
func (s *Service) DeleteTelegram(ctx context.Context, principal authz.Principal) error {
	if !authz.Can(principal, authz.ActionChannelsManage, 0) {
		return ErrForbidden
	}
	return s.store.Tx(ctx, func(tx pgx.Tx) error {
		var id int64
		err := tx.QueryRow(ctx, `delete from org_notification_channels where org_id=$1 and kind='telegram' returning id`, principal.OrgID).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("delete notification channel: %w", err)
		}
		return audit.Write(ctx, tx, principal, "org_notification_channel.deleted", "org_notification_channel", id, map[string]any{"kind": KindTelegram})
	})
}

// TestTelegram sends a test message now, ignoring the enabled flag and the
// event map. At most one per org every 30 seconds.
func (s *Service) TestTelegram(ctx context.Context, principal authz.Principal) error {
	if !authz.Can(principal, authz.ActionChannelsManage, 0) {
		return ErrForbidden
	}
	var id int64
	var sealed []byte
	var chat string
	err := s.pool.QueryRow(ctx, `select id,config_enc,chat_id from org_notification_channels where org_id=$1 and kind='telegram'`, principal.OrgID).Scan(&id, &sealed, &chat)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load notification channel: %w", err)
	}
	now := s.now()
	s.mu.Lock()
	if last, ok := s.lastTest[principal.OrgID]; ok && now.Sub(last) < TestInterval {
		s.mu.Unlock()
		return ErrTooSoon
	}
	s.lastTest[principal.OrgID] = now
	s.mu.Unlock()

	plain, err := s.box.Open(sealed)
	if err != nil {
		return &DeliveryError{Message: "the stored bot token cannot be read; save it again"}
	}
	token := string(plain)
	sendErr := s.sender.SendMessage(ctx, token, chat, "Supermetry test message: notifications for this organization are working.")
	var scrubbed string
	if sendErr != nil {
		scrubbed = Scrub(sendErr.Error(), token)
		_, _ = s.pool.Exec(ctx, `update org_notification_channels set last_error=$2 where id=$1`, id, scrubbed)
	} else {
		_, _ = s.pool.Exec(ctx, `update org_notification_channels set last_ok_at=$2, last_error='', consecutive_failures=0 where id=$1`, id, now)
	}
	status := "succeeded"
	if sendErr != nil {
		status = "failed"
	}
	_ = s.store.Tx(ctx, func(tx pgx.Tx) error {
		return audit.Write(ctx, tx, principal, "org_notification_channel.tested", "org_notification_channel", id, map[string]any{"kind": KindTelegram, "status": status})
	})
	if sendErr != nil {
		return &DeliveryError{Message: scrubbed}
	}
	return nil
}

type target struct {
	id     int64
	sealed []byte
	chat   string
}

// Notify implements Notifier. It claims the event synchronously (dedup and
// throttle, one short transaction) and delivers in the background.
func (s *Service) Notify(ctx context.Context, orgID int64, event Event) {
	if orgID <= 0 || !Valid(event.Kind) || event.Key == "" || strings.TrimSpace(event.Title+event.Body) == "" {
		return
	}
	targets, claimed, err := s.claim(ctx, orgID, event)
	if err != nil {
		slog.Warn("org notification claim failed", "org_id", orgID, "kind", event.Kind, "error", err)
		return
	}
	if !claimed || len(targets) == 0 {
		return
	}
	select {
	case s.inFlight <- struct{}{}:
	default:
		s.release(orgID, event)
		slog.Warn("org notification dropped: too many deliveries in flight", "org_id", orgID, "kind", event.Kind)
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() { <-s.inFlight }()
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deliverTimeout)
		defer cancel()
		if !s.deliver(dctx, orgID, event, targets) {
			s.release(orgID, event)
		}
	}()
}

// Drain waits for in-flight deliveries (tests and graceful shutdown).
func (s *Service) Drain(ctx context.Context) error {
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// claim finds the enabled channels that opted into the kind and, if there are
// any, records the event unless it is a duplicate or inside the throttle
// window. Orgs with no interested channel record nothing, so enabling a kind
// later does not find old keys already used up.
func (s *Service) claim(ctx context.Context, orgID int64, event Event) ([]target, bool, error) {
	var targets []target
	claimed := false
	err := s.store.Tx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `select id,config_enc,chat_id from org_notification_channels
			where org_id=$1 and enabled and events->>$2 = 'true' order by id`, orgID, string(event.Kind))
		if err != nil {
			return err
		}
		for rows.Next() {
			var t target
			if err := rows.Scan(&t.id, &t.sealed, &t.chat); err != nil {
				rows.Close()
				return err
			}
			targets = append(targets, t)
		}
		rows.Close()
		if err := rows.Err(); err != nil || len(targets) == 0 {
			return err
		}
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("org-notify:%d:%s", orgID, event.Kind)); err != nil {
			return err
		}
		now := s.now()
		var recent bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from org_notification_log where org_id=$1 and kind=$2 and sent_at > $3)`,
			orgID, string(event.Kind), now.Add(-s.minInterval)).Scan(&recent); err != nil {
			return err
		}
		if recent {
			return nil
		}
		tag, err := tx.Exec(ctx, `insert into org_notification_log(org_id,kind,key,sent_at) values ($1,$2,$3,$4) on conflict do nothing`,
			orgID, string(event.Kind), truncate(event.Key, 200), now)
		if err != nil {
			return err
		}
		claimed = tag.RowsAffected() == 1
		if claimed {
			_, err = tx.Exec(ctx, `delete from org_notification_log where org_id=$1 and kind=$2 and sent_at < $3`, orgID, string(event.Kind), now.Add(-logRetention))
		}
		return err
	})
	return targets, claimed, err
}

// release forgets a claim after every delivery failed, so the next event is
// neither throttled nor deduplicated against a message nobody received.
func (s *Service) release(orgID int64, event Event) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = s.pool.Exec(ctx, `delete from org_notification_log where org_id=$1 and kind=$2 and key=$3`, orgID, string(event.Kind), truncate(event.Key, 200))
}

// deliver sends to every target and reports whether at least one succeeded.
func (s *Service) deliver(ctx context.Context, orgID int64, event Event, targets []target) bool {
	text := strings.TrimSpace(event.Title)
	if body := strings.TrimSpace(event.Body); body != "" {
		if text != "" {
			text += "\n\n"
		}
		text += body
	}
	anyOK := false
	for _, t := range targets {
		plain, err := s.box.Open(t.sealed)
		if err != nil {
			s.recordFailure(ctx, t.id, "the stored bot token cannot be read; save it again")
			continue
		}
		token := string(plain)
		if err := s.sender.SendMessage(ctx, token, t.chat, text); err != nil {
			s.recordFailure(ctx, t.id, Scrub(err.Error(), token))
			continue
		}
		anyOK = true
		if _, err := s.pool.Exec(ctx, `update org_notification_channels set last_ok_at=$2, last_error='', consecutive_failures=0 where id=$1`, t.id, s.now()); err != nil {
			slog.Warn("org notification status update failed", "org_id", orgID, "error", err)
		}
	}
	return anyOK
}

func (s *Service) recordFailure(ctx context.Context, id int64, message string) {
	_, err := s.pool.Exec(ctx, `update org_notification_channels set
			consecutive_failures = consecutive_failures + 1,
			last_error = case when consecutive_failures + 1 >= $3
				then left('disabled after '||$3::text||' consecutive failures; last error: '||$2, 400) else $2 end,
			enabled = case when consecutive_failures + 1 >= $3 then false else enabled end
		where id=$1`, id, message, MaxConsecutiveFailures)
	if err != nil {
		slog.Warn("org notification failure update failed", "error", err)
	}
}
