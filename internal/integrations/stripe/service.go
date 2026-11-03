package stripe

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/dict"
	"supermetry/internal/integrations"
	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/platform/store"
	"supermetry/internal/privacy"
	"supermetry/internal/salts"
	"supermetry/internal/scheduler"
	"supermetry/internal/session"
	"supermetry/internal/sites"
	"supermetry/internal/writer"
)

// Retention and limits.
const (
	WebhookPath = "/api/public/v1/integrations/stripe/webhook/"
	// EventRetention is how long handled Stripe event ids are kept. Stripe
	// retries a delivery for up to three days.
	EventRetention = 30 * 24 * time.Hour
	// CustomerRetention drops the channel of customers without a payment for
	// this long, a little over a yearly renewal.
	CustomerRetention = 400 * 24 * time.Hour
	// RefundRetention bounds the refund ledger.
	RefundRetention = 400 * 24 * time.Hour
	// MaxEventAge is the oldest Stripe "created" time used as the event time;
	// older deliveries (retries, resends) are recorded at receipt time.
	MaxEventAge     = 24 * time.Hour
	maxErrorBytes   = 200
	maxSecretBytes  = 256
	statusLookback  = 30 * 24 * time.Hour
	endpointIDBytes = 32
)

var (
	endpointPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	secretPattern   = regexp.MustCompile(`^whsec_[A-Za-z0-9+/=_-]{16,240}$`)
)

// Sink accepts revenue events. *writer.Writer satisfies it through WriterSink.
type Sink interface {
	Reserve() (Slot, bool)
}

// Slot is a reserved writer capacity unit; exactly one of Commit or Cancel runs.
type Slot interface {
	Commit(writer.Event) bool
	Cancel()
}

// WriterSink adapts the event writer.
type WriterSink struct{ Writer *writer.Writer }

// Reserve claims writer capacity.
func (s WriterSink) Reserve() (Slot, bool) {
	reservation, ok := s.Writer.ReserveEvent()
	if !ok {
		return nil, false
	}
	return reservation, true
}

// Dictionary interns dimension values.
type Dictionary interface {
	IDsForSite(ctx context.Context, siteID int32, kind dict.Kind, values []string) ([]int32, error)
}

// Deps are the collaborators of a Service.
type Deps struct {
	Store *store.Store
	// Secrets seals the webhook signing secrets.
	Secrets    *platformcrypto.SecretBox
	Tokens     *Tokens
	Sites      sites.Registry
	Dictionary Dictionary
	Sink       Sink
	Mode       privacy.Mode
	// BaseURL is the public origin used in the webhook URL shown to users.
	BaseURL string
	Now     func() time.Time
	Logger  *slog.Logger
}

// Service owns Stripe connections and turns verified webhooks into revenue.
type Service struct {
	store   *store.Store
	secrets *platformcrypto.SecretBox
	tokens  *Tokens
	sites   sites.Registry
	dict    Dictionary
	sink    Sink
	mode    privacy.Mode
	baseURL string
	now     func() time.Time
	logger  *slog.Logger
	// decoy is verified for unknown endpoints so they cost the same work as
	// known ones.
	decoy []byte
}

// New returns a service.
func New(deps Deps) *Service {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	decoy := make([]byte, 32)
	_, _ = rand.Read(decoy)
	return &Service{
		store: deps.Store, secrets: deps.Secrets, tokens: deps.Tokens, sites: deps.Sites, dict: deps.Dictionary,
		sink: deps.Sink, mode: deps.Mode, baseURL: strings.TrimRight(deps.BaseURL, "/"), now: now, logger: logger,
		decoy: []byte("whsec_" + base64.RawStdEncoding.EncodeToString(decoy)),
	}
}

// Status is the dashboard view of a site's Stripe connection.
type Status struct {
	Enabled              bool       `json:"enabled"`
	WebhookURL           string     `json:"webhook_url,omitempty"`
	SecretConfigured     bool       `json:"secret_configured"`
	RecordPaymentIntents bool       `json:"record_payment_intents"`
	Events               []string   `json:"events"`
	OptionalEvents       []string   `json:"optional_events"`
	RevenueCurrency      string     `json:"revenue_currency,omitempty"`
	EventsReceived       int64      `json:"events_received"`
	RevenueEvents        int64      `json:"revenue_events"`
	Refunds30d           int64      `json:"refunds_30d"`
	RefundedMicros30d    int64      `json:"refunded_micros_30d"`
	LastEventAt          *time.Time `json:"last_event_at,omitempty"`
	LastEventType        string     `json:"last_event_type,omitempty"`
	LastEventLivemode    *bool      `json:"last_event_livemode,omitempty"`
	LastError            string     `json:"last_error,omitempty"`
	LastErrorAt          *time.Time `json:"last_error_at,omitempty"`
	CreatedAt            *time.Time `json:"created_at,omitempty"`
}

// Update changes a connection. Nil fields are left as they are.
type Update struct {
	SigningSecret        *string `json:"signing_secret"`
	RecordPaymentIntents *bool   `json:"record_payment_intents"`
}

func (s *Service) manage(p authz.Principal, siteID int32) bool {
	return authz.Can(p, authz.ActionSiteEdit, siteID) && (p.Role == authz.RoleOwner || p.Role == authz.RoleAdmin)
}

func (s *Service) siteInOrg(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, orgID int64, siteID int32) error {
	var ok bool
	if err := q.QueryRow(ctx, `select exists(select 1 from sites where id=$1 and org_id=$2 and deleted_at is null)`, siteID, orgID).Scan(&ok); err != nil {
		return fmt.Errorf("check site: %w", err)
	}
	if !ok {
		return integrations.ErrNotFound
	}
	return nil
}

// WebhookURL is the endpoint URL to paste into Stripe. Without a configured
// base URL it is a path, which the dashboard completes with its origin.
func (s *Service) WebhookURL(endpointID string) string {
	return s.baseURL + WebhookPath + endpointID
}

// Get returns the connection status. Members who may see integrations may read it.
func (s *Service) Get(ctx context.Context, p authz.Principal, siteID int32) (Status, error) {
	if !authz.Can(p, authz.ActionSiteEdit, siteID) || p.Role == authz.RoleKey {
		return Status{}, integrations.ErrForbidden
	}
	if err := s.siteInOrg(ctx, s.store.Pool, p.OrgID, siteID); err != nil {
		return Status{}, err
	}
	return s.status(ctx, siteID)
}

func (s *Service) status(ctx context.Context, siteID int32) (Status, error) {
	status := Status{Events: HandledEvents, OptionalEvents: OptionalEvents}
	if site, ok := s.sites.Site(siteID); ok && site.RevenueCurrencyState == sites.RevenueCurrencyConfigured {
		status.RevenueCurrency = site.RevenueCurrency
	}
	var (
		endpointID string
		secret     []byte
		created    time.Time
	)
	err := s.store.Pool.QueryRow(ctx, `select endpoint_id,signing_secret,record_payment_intents,events_received,revenue_events,
		last_event_at,last_event_type,last_event_livemode,last_error,last_error_at,created_at
		from stripe_connections where site_id=$1`, siteID).Scan(&endpointID, &secret, &status.RecordPaymentIntents,
		&status.EventsReceived, &status.RevenueEvents, &status.LastEventAt, &status.LastEventType, &status.LastEventLivemode,
		&status.LastError, &status.LastErrorAt, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return status, nil
	}
	if err != nil {
		return Status{}, fmt.Errorf("load stripe connection: %w", err)
	}
	status.Enabled, status.WebhookURL, status.SecretConfigured, status.CreatedAt = true, s.WebhookURL(endpointID), len(secret) > 0, &created
	if status.RevenueCurrency != "" {
		if err := s.store.Pool.QueryRow(ctx, `select count(*),coalesce(sum(amount_micros),0)::bigint from stripe_refunds
			where site_id=$1 and refunded_at>=$2 and currency=$3`, siteID, s.now().Add(-statusLookback), status.RevenueCurrency).
			Scan(&status.Refunds30d, &status.RefundedMicros30d); err != nil {
			return Status{}, fmt.Errorf("load stripe refunds: %w", err)
		}
	}
	return status, nil
}

// Enable creates the site's webhook endpoint. It is idempotent: an existing
// connection is returned unchanged.
func (s *Service) Enable(ctx context.Context, p authz.Principal, siteID int32) (Status, error) {
	if !s.manage(p, siteID) {
		return Status{}, integrations.ErrForbidden
	}
	if site, ok := s.sites.Site(siteID); ok && site.Demo {
		return Status{}, fmt.Errorf("%w: demo sites cannot receive Stripe payments", integrations.ErrInvalid)
	}
	endpoint := make([]byte, endpointIDBytes)
	hashKey := make([]byte, 32)
	if _, err := rand.Read(endpoint); err != nil {
		return Status{}, err
	}
	if _, err := rand.Read(hashKey); err != nil {
		return Status{}, err
	}
	err := s.store.Tx(ctx, func(tx pgx.Tx) error {
		if err := s.siteInOrg(ctx, tx, p.OrgID, siteID); err != nil {
			return err
		}
		var connectedBy *int64
		if p.UserID != 0 {
			connectedBy = &p.UserID
		}
		tag, err := tx.Exec(ctx, `insert into stripe_connections(site_id,org_id,endpoint_id,hash_key,connected_by)
			values($1,$2,$3,$4,$5) on conflict (site_id) do nothing`,
			siteID, p.OrgID, base64.RawURLEncoding.EncodeToString(endpoint), hashKey, connectedBy)
		if err != nil {
			return fmt.Errorf("create stripe connection: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		return audit.Write(ctx, tx, p, "integration.connected", "stripe_connection", int64(siteID), map[string]any{"provider": "stripe", "site_id": siteID})
	})
	if err != nil {
		return Status{}, err
	}
	return s.status(ctx, siteID)
}

// Configure stores the signing secret and options of an enabled connection.
func (s *Service) Configure(ctx context.Context, p authz.Principal, siteID int32, update Update) (Status, error) {
	if !s.manage(p, siteID) {
		return Status{}, integrations.ErrForbidden
	}
	var sealed []byte
	if update.SigningSecret != nil {
		secret := strings.TrimSpace(*update.SigningSecret)
		if len(secret) > maxSecretBytes || !secretPattern.MatchString(secret) {
			return Status{}, fmt.Errorf("%w: the signing secret must be the whsec_ value from the Stripe webhook endpoint", integrations.ErrInvalid)
		}
		sealed = s.secrets.Seal([]byte(secret))
	}
	err := s.store.Tx(ctx, func(tx pgx.Tx) error {
		if err := s.siteInOrg(ctx, tx, p.OrgID, siteID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `update stripe_connections set
			signing_secret=coalesce($2,signing_secret),
			record_payment_intents=coalesce($3,record_payment_intents),
			last_error=case when $2::bytea is null then last_error else '' end,
			last_error_at=case when $2::bytea is null then last_error_at else null end,
			updated_at=now()
			where site_id=$1 and org_id=$4`, siteID, sealed, update.RecordPaymentIntents, p.OrgID)
		if err != nil {
			return fmt.Errorf("update stripe connection: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return integrations.ErrNotFound
		}
		metadata := map[string]any{"provider": "stripe", "site_id": siteID, "signing_secret_replaced": sealed != nil}
		if update.RecordPaymentIntents != nil {
			metadata["record_payment_intents"] = *update.RecordPaymentIntents
		}
		return audit.Write(ctx, tx, p, "integration.updated", "stripe_connection", int64(siteID), metadata)
	})
	if err != nil {
		return Status{}, err
	}
	return s.status(ctx, siteID)
}

// Disconnect deletes the endpoint, the sealed secret, the customer channels,
// the refund ledger and the handled event ids. Revenue events already
// recorded stay, like any other analytics event.
func (s *Service) Disconnect(ctx context.Context, p authz.Principal, siteID int32) error {
	if !s.manage(p, siteID) {
		return integrations.ErrForbidden
	}
	return s.store.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `delete from stripe_connections where site_id=$1 and org_id=$2`, siteID, p.OrgID)
		if err != nil {
			return fmt.Errorf("delete stripe connection: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return integrations.ErrNotFound
		}
		return audit.Write(ctx, tx, p, "integration.disconnected", "stripe_connection", int64(siteID), map[string]any{"provider": "stripe", "site_id": siteID})
	})
}

// RetentionJob prunes handled event ids, idle customer channels and old refunds daily.
func (s *Service) RetentionJob() scheduler.Job {
	return scheduler.Job{Name: "stripe-retention", Every: 24 * time.Hour, Run: s.Retention}
}

// Retention deletes rows past their retention.
func (s *Service) Retention(ctx context.Context) error {
	now := s.now()
	for _, q := range []struct {
		sql    string
		before time.Time
	}{
		{`delete from stripe_webhook_events where received_at<$1`, now.Add(-EventRetention)},
		{`delete from stripe_customers where last_seen_at<$1`, now.Add(-CustomerRetention)},
		{`delete from stripe_refunds where refunded_at<$1`, now.Add(-RefundRetention)},
	} {
		if _, err := s.store.Pool.Exec(ctx, q.sql, q.before); err != nil {
			return fmt.Errorf("stripe retention: %w", err)
		}
	}
	return nil
}

type connection struct {
	siteID               int32
	secret               []byte
	hashKey              []byte
	recordPaymentIntents bool
}

func (s *Service) connection(ctx context.Context, endpointID string) (connection, bool, error) {
	if !endpointPattern.MatchString(endpointID) {
		return connection{}, false, nil
	}
	var (
		c      connection
		sealed []byte
	)
	err := s.store.Pool.QueryRow(ctx, `select site_id,signing_secret,hash_key,record_payment_intents from stripe_connections where endpoint_id=$1`, endpointID).
		Scan(&c.siteID, &sealed, &c.hashKey, &c.recordPaymentIntents)
	if errors.Is(err, pgx.ErrNoRows) {
		return connection{}, false, nil
	}
	if err != nil {
		return connection{}, false, err
	}
	if len(sealed) == 0 {
		return c, false, nil
	}
	secret, err := s.secrets.Open(sealed)
	if err != nil {
		return connection{}, false, fmt.Errorf("open stripe signing secret: %w", err)
	}
	c.secret = secret
	return c, true, nil
}

// HandleWebhook verifies and processes one delivery and returns the HTTP
// status for Stripe: 2xx when handled or deliberately skipped, 400 for an
// unknown endpoint or a bad signature (the two are indistinguishable), and
// 5xx when Stripe should retry.
func (s *Service) HandleWebhook(ctx context.Context, endpointID, signature string, body []byte) int {
	now := s.now()
	c, usable, err := s.connection(ctx, endpointID)
	if err != nil {
		s.logger.Error("stripe webhook lookup failed", "error", err)
		return http.StatusInternalServerError
	}
	secret := c.secret
	if !usable {
		secret = s.decoy
	}
	verifyErr := VerifySignature(body, signature, secret, now, SignatureTolerance)
	if !usable {
		if c.siteID != 0 {
			s.recordError(ctx, c.siteID, "a delivery arrived before the signing secret was saved")
		}
		return http.StatusBadRequest
	}
	if verifyErr != nil {
		s.recordError(ctx, c.siteID, "signature verification failed: "+verifyErr.Error())
		return http.StatusBadRequest
	}
	envelope, err := ParseEnvelope(body)
	if err != nil {
		s.recordError(ctx, c.siteID, err.Error())
		return http.StatusBadRequest
	}
	action, err := MapEvent(envelope, MapOptions{RecordPaymentIntents: c.recordPaymentIntents})
	if err != nil {
		s.recordError(ctx, c.siteID, envelope.Type+": "+err.Error())
		return http.StatusBadRequest
	}
	site, ok := s.sites.Site(c.siteID)
	if !ok || site.Demo || site.TZ == nil {
		// The site is being deleted; its connection cascades with it.
		return http.StatusBadRequest
	}
	return s.process(ctx, c, site, envelope, action, now)
}

func (s *Service) process(ctx context.Context, c connection, site sites.Site, envelope Envelope, action Action, now time.Time) int {
	outcome := outcome{}
	var micros int64
	var currency string
	if action.Kind == ActionRevenue || action.Kind == ActionRefund {
		var err error
		micros, currency, err = MinorToMicros(action.AmountMinor, action.Currency)
		if err != nil {
			outcome.err = envelope.Type + ": " + err.Error()
			action.Kind = ActionIgnore
		}
	}
	if !envelope.Livemode && action.Kind != ActionIgnore {
		// Test-mode events prove the connection works but never become data.
		action.Kind = ActionIgnore
	}
	if action.Kind == ActionRevenue && outcome.err == "" {
		switch {
		case site.RevenueCurrencyState != sites.RevenueCurrencyConfigured || site.RevenueCurrency == "":
			outcome.err = "the site has no revenue currency; set one in the site settings"
			action.Kind = ActionIgnore
		case currency != site.RevenueCurrency:
			outcome.err = fmt.Sprintf("payment in %s was not recorded: the site's revenue currency is %s", currency, site.RevenueCurrency)
			action.Kind = ActionIgnore
		}
	}

	var slot Slot
	if action.Kind == ActionRevenue {
		var ok bool
		if slot, ok = s.sink.Reserve(); !ok {
			return http.StatusServiceUnavailable
		}
		defer slot.Cancel()
	}

	eventTime := now
	if created := time.Unix(envelope.Created, 0); envelope.Created > 0 && now.Sub(created) <= MaxEventAge && created.Sub(now) <= 5*time.Minute {
		eventTime = created
	}
	identity := s.identityAllowed(site)

	var event writer.Event
	var channel *customerChannel
	if action.Kind == ActionRevenue || action.Kind == ActionCustomer {
		attribution, attributed := Attribution{}, false
		if action.Token != "" && s.tokens != nil {
			if a, err := s.tokens.Open(action.Token, site.ID, now); err == nil {
				attribution, attributed = a, true
			}
		}
		if attributed && identity && action.Customer != "" {
			values, err := s.dimensionValues(ctx, attribution)
			if err != nil {
				s.logger.Error("stripe attribution lookup failed", "error", err)
				return http.StatusInternalServerError
			}
			values.product = action.Product
			channel = &values
		}
		if action.Kind == ActionRevenue {
			if !attributed && identity && action.Customer != "" {
				stored, found, err := s.storedChannel(ctx, c, action.Customer)
				if err != nil {
					s.logger.Error("stripe customer lookup failed", "error", err)
					return http.StatusInternalServerError
				}
				if found {
					ids, err := s.channelIDs(ctx, site.ID, stored)
					if err != nil {
						s.logger.Error("stripe dictionary lookup failed", "error", err)
						return http.StatusInternalServerError
					}
					attribution = ids
					if action.Product == "" && stored.product != "" {
						action.Product = stored.product
						action.Props["product"] = stored.product
					}
				}
			}
			var err error
			event, err = s.buildEvent(ctx, c, site, action, attribution, micros, currency, eventTime)
			if err != nil {
				s.logger.Error("stripe event build failed", "error", err)
				return http.StatusInternalServerError
			}
		}
	}

	handled := false
	err := s.store.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `insert into stripe_webhook_events(site_id,event_id,received_at) values($1,$2,$3) on conflict do nothing`, site.ID, envelope.ID, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		handled = true
		if channel != nil {
			if _, err := tx.Exec(ctx, `insert into stripe_customers(site_id,customer_key,source,utm_source,medium,campaign,country,product,last_seen_at)
				values($1,$2,$3,$4,$5,$6,$7,$8,$9)
				on conflict (site_id,customer_key) do update set source=excluded.source,utm_source=excluded.utm_source,medium=excluded.medium,
				campaign=excluded.campaign,country=excluded.country,product=case when excluded.product<>'' then excluded.product else stripe_customers.product end,
				last_seen_at=excluded.last_seen_at`,
				site.ID, keyedHash(c.hashKey, "customer", action.Customer), channel.source, channel.utmSource, channel.medium,
				channel.campaign, channel.country, channel.product, now); err != nil {
				return err
			}
		} else if action.Kind == ActionRevenue && action.Customer != "" {
			if _, err := tx.Exec(ctx, `update stripe_customers set last_seen_at=$3 where site_id=$1 and customer_key=$2`,
				site.ID, keyedHash(c.hashKey, "customer", action.Customer), now); err != nil {
				return err
			}
		}
		if action.Kind == ActionRefund {
			if _, err := tx.Exec(ctx, `insert into stripe_refunds(site_id,charge_key,amount_micros,currency,refunded_at) values($1,$2,$3,$4,$5)
				on conflict (site_id,charge_key) do update set amount_micros=greatest(stripe_refunds.amount_micros,excluded.amount_micros),
				currency=excluded.currency,refunded_at=excluded.refunded_at`,
				site.ID, keyedHash(c.hashKey, "charge", action.ObjectID), micros, currency, eventTime); err != nil {
				return err
			}
		}
		revenue := 0
		if action.Kind == ActionRevenue {
			revenue = 1
		}
		_, err = tx.Exec(ctx, `update stripe_connections set events_received=events_received+1,revenue_events=revenue_events+$2,
			last_event_at=$3::timestamptz,last_event_type=$4,last_event_livemode=$5,
			last_error=$6::text,last_error_at=case when $6::text<>'' then $3::timestamptz end,updated_at=$3::timestamptz
			where site_id=$1`, site.ID, revenue, now, envelope.Type, envelope.Livemode, truncate(outcome.err, maxErrorBytes))
		return err
	})
	if err != nil {
		s.logger.Error("stripe webhook processing failed", "error", err)
		return http.StatusInternalServerError
	}
	if handled && slot != nil {
		slot.Commit(event)
	}
	return http.StatusOK
}

type outcome struct {
	err string
}

type customerChannel struct {
	source, utmSource, medium, campaign, country, product string
}

func (s *Service) identityAllowed(site sites.Site) bool {
	return s.mode == privacy.Identified && site.ProfilesEnabled && !site.Demo
}

// dimensionValues resolves the token's dictionary ids to the strings stored
// for later renewals.
func (s *Service) dimensionValues(ctx context.Context, a Attribution) (customerChannel, error) {
	channel := customerChannel{country: a.Country}
	for _, item := range []struct {
		kind dict.Kind
		id   int32
		dest *string
	}{
		{dict.Source, a.Source, &channel.source},
		{dict.Source, a.UTMSource, &channel.utmSource},
		{dict.Medium, a.Medium, &channel.medium},
		{dict.Campaign, a.Campaign, &channel.campaign},
	} {
		if item.id == 0 {
			continue
		}
		var value string
		err := s.store.Pool.QueryRow(ctx, `select value from dict where kind=$1 and id=$2`, int16(item.kind), item.id).Scan(&value)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return customerChannel{}, err
		}
		*item.dest = truncate(value, 512)
	}
	return channel, nil
}

func (s *Service) storedChannel(ctx context.Context, c connection, customer string) (customerChannel, bool, error) {
	var channel customerChannel
	err := s.store.Pool.QueryRow(ctx, `select source,utm_source,medium,campaign,country,product from stripe_customers where site_id=$1 and customer_key=$2`,
		c.siteID, keyedHash(c.hashKey, "customer", customer)).
		Scan(&channel.source, &channel.utmSource, &channel.medium, &channel.campaign, &channel.country, &channel.product)
	if errors.Is(err, pgx.ErrNoRows) {
		return customerChannel{}, false, nil
	}
	return channel, err == nil, err
}

func (s *Service) channelIDs(ctx context.Context, siteID int32, channel customerChannel) (Attribution, error) {
	a := Attribution{SiteID: siteID, Country: channel.country}
	for _, item := range []struct {
		kind  dict.Kind
		value string
		dest  *int32
	}{
		{dict.Source, channel.source, &a.Source},
		{dict.Source, channel.utmSource, &a.UTMSource},
		{dict.Medium, channel.medium, &a.Medium},
		{dict.Campaign, channel.campaign, &a.Campaign},
	} {
		if item.value == "" {
			continue
		}
		ids, err := s.dict.IDsForSite(ctx, siteID, item.kind, []string{item.value})
		if err != nil {
			return Attribution{}, err
		}
		*item.dest = ids[0]
	}
	return a, nil
}

func (s *Service) buildEvent(ctx context.Context, c connection, site sites.Site, action Action, a Attribution, micros int64, currency string, at time.Time) (writer.Event, error) {
	names, err := s.dict.IDsForSite(ctx, site.ID, dict.EventName, []string{action.Name})
	if err != nil {
		return writer.Event{}, err
	}
	visitorID := a.VisitorID
	if visitorID == 0 {
		// Unattributed payments get a visitor id of their own, derived from
		// the Stripe object, so they never merge into a real visitor.
		sum := keyedHash(c.hashKey, "visitor", action.ObjectID)
		visitorID = int64(binary.BigEndian.Uint64(sum[:8]) &^ (1 << 63))
	}
	country := a.Country
	if !validCountry(country) {
		country = ""
	}
	return writer.Event{
		Hit: session.Hit{
			SiteID: site.ID, Retention: site.Retention, TS: at.UTC(), LocalDay: salts.LocalDay(at, site.TZ),
			VisitorID: visitorID, Kind: 1, NameID: names[0],
			Source: a.Source, UTMSource: a.UTMSource, Medium: a.Medium, Campaign: a.Campaign,
			Country: country, Region: a.Region,
		},
		SessionID:         a.SessionID,
		ProfileExternalID: action.Profile,
		Props:             action.Props,
		RevenueMicros:     micros,
		RevenueCurrency:   currency,
	}, nil
}

func validCountry(country string) bool {
	return len(country) == 2 && country[0] >= 'A' && country[0] <= 'Z' && country[1] >= 'A' && country[1] <= 'Z'
}

func (s *Service) recordError(ctx context.Context, siteID int32, message string) {
	if _, err := s.store.Pool.Exec(ctx, `update stripe_connections set last_error=$2,last_error_at=$3,updated_at=$3 where site_id=$1`,
		siteID, truncate(message, maxErrorBytes), s.now()); err != nil {
		s.logger.Error("stripe status update failed", "error", err)
	}
}

func keyedHash(key []byte, purpose, value string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(purpose))
	mac.Write([]byte{0})
	mac.Write([]byte(value))
	return mac.Sum(nil)
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
