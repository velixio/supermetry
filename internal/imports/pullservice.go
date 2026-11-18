package imports

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"regexp"
	"strings"
	"time"

	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/safehttp"
)

// PullOptions configure a PullService.
type PullOptions struct {
	HTTP PullHTTPOptions
	GA4  GA4Config
	// Resolver looks hosts up for the early base-URL check; nil uses the
	// system resolver. Dialing re-checks every address regardless.
	Resolver safehttp.Resolver
	Now      func() time.Time
	// PageSize overrides the Plausible page size (tests).
	PageSize int
}

// PullService validates, previews and runs pull imports and owns the GA4
// OAuth connections. Secrets are sealed with the SecretBox it is given.
type PullService struct {
	store    *Store
	box      *platformcrypto.SecretBox
	client   *pullClient
	ga4      GA4Config
	resolver safehttp.Resolver
	allowRFC bool
	now      func() time.Time
	pageSize int
}

// NewPullService builds the service. box must be derived with its own purpose
// string so import secrets cannot be opened by another subsystem.
func NewPullService(store *Store, box *platformcrypto.SecretBox, opts PullOptions) *PullService {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Resolver == nil {
		opts.Resolver = net.DefaultResolver
	}
	if opts.PageSize <= 0 {
		opts.PageSize = plausiblePageSize
	}
	return &PullService{
		store: store, box: box, client: newPullClient(opts.HTTP), ga4: opts.GA4.withDefaults(),
		resolver: opts.Resolver, allowRFC: opts.HTTP.AllowPrivate, now: opts.Now, pageSize: opts.PageSize,
	}
}

// Capabilities tells the wizard which pull sources can be used.
type Capabilities struct {
	PlausibleAPI SourceCapability `json:"plausible_api"`
	MatomoAPI    SourceCapability `json:"matomo_api"`
	GA4API       SourceCapability `json:"ga4_api"`
}

// SourceCapability is one source's availability. Reason is set when it is not
// available and names the missing configuration.
type SourceCapability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// Capabilities reports what this installation can pull.
func (s *PullService) Capabilities() Capabilities {
	result := Capabilities{PlausibleAPI: SourceCapability{Available: true}, MatomoAPI: SourceCapability{Available: true}}
	if s.ga4.Configured() {
		result.GA4API.Available = true
	} else {
		result.GA4API.Reason = "Google Analytics import is not configured: set SUPERMETRY_GA4_OAUTH_CLIENT_ID and SUPERMETRY_GA4_OAUTH_CLIENT_SECRET"
	}
	return result
}

var (
	plausibleSitePattern = regexp.MustCompile(`^[A-Za-z0-9._:/-]{1,253}$`)
	matomoSitePattern    = regexp.MustCompile(`^[0-9]{1,9}$`)
)

const maxSecretBytes = 512

// validate normalises a request and returns its day range.
func (s *PullService) validate(ctx context.Context, orgID int64, request *PullRequest) (DayRange, error) {
	rng, err := request.Range()
	if err != nil {
		return rng, err
	}
	invalid := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrPullInvalid, fmt.Sprintf(format, args...))
	}
	switch request.Method {
	case MethodPlausibleAPI, MethodMatomoAPI:
		request.BaseURL = strings.TrimSpace(request.BaseURL)
		if request.BaseURL == "" && request.Method == MethodPlausibleAPI {
			request.BaseURL = DefaultPlausibleURL
		}
		base, err := ParseBaseURL(request.BaseURL)
		if err != nil {
			return rng, invalid("%v", err)
		}
		request.BaseURL = base
		request.Secret = strings.TrimSpace(request.Secret)
		if request.Secret == "" || len(request.Secret) > maxSecretBytes {
			return rng, invalid("an API key or token is required")
		}
		request.RemoteSiteID = strings.TrimSpace(request.RemoteSiteID)
		pattern := plausibleSitePattern
		label := "site_id"
		if request.Method == MethodMatomoAPI {
			pattern, label = matomoSitePattern, "idSite"
		}
		if !pattern.MatchString(request.RemoteSiteID) {
			return rng, invalid("%s is missing or malformed", label)
		}
		if err := s.checkHost(ctx, base); err != nil {
			return rng, err
		}
		request.Property, request.ConnectionID = "", 0
	case MethodGA4API:
		if !s.ga4.Configured() {
			return rng, ErrGA4NotConfigured
		}
		if !ga4PropertyPattern.MatchString(request.Property) {
			return rng, invalid("property must look like properties/123456789")
		}
		if request.ConnectionID < 1 {
			return rng, invalid("connection_id is required")
		}
		if _, err := s.ga4RefreshToken(ctx, orgID, request.ConnectionID); err != nil {
			return rng, err
		}
		request.BaseURL, request.RemoteSiteID, request.Secret = "", "", ""
	default:
		return rng, invalid("method must be plausible_api, matomo_api or ga4_api")
	}
	return rng, nil
}

// checkHost rejects a base URL whose host is, or resolves to, a private or
// internal address, so the user gets an immediate answer. The dialer repeats
// the check for every connection, which is what closes DNS rebinding.
func (s *PullService) checkHost(ctx context.Context, base string) error {
	if s.allowRFC {
		return nil
	}
	host := hostOf(base)
	var addresses []netip.Addr
	if literal, err := netip.ParseAddr(host); err == nil {
		addresses = []netip.Addr{literal}
	} else {
		resolved, err := s.resolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(resolved) == 0 {
			return fmt.Errorf("%w: the host %q could not be resolved", ErrPullInvalid, host)
		}
		addresses = resolved
	}
	for _, address := range addresses {
		if safehttp.BlockedAddress(address) {
			return fmt.Errorf("%w: the base URL points at a private or internal address", ErrPullInvalid)
		}
	}
	return nil
}

func hostOf(base string) string {
	rest := base[strings.Index(base, "://")+3:]
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	host, _, err := net.SplitHostPort(rest)
	if err != nil {
		host = rest
	}
	return strings.Trim(host, "[]")
}

// Create validates a request, stores it with sealed credentials and returns
// the pending import. The caller queues a Job{Pull: true} for it.
func (s *PullService) Create(ctx context.Context, siteID int32, orgID, userID int64, request PullRequest) (Import, error) {
	rng, err := s.validate(ctx, orgID, &request)
	if err != nil {
		return Import{}, err
	}
	var sealed []byte
	if request.Secret != "" {
		sealed = s.box.Seal([]byte(request.Secret))
	}
	return s.store.CreatePull(ctx, siteID, userID, request, sealed, rng.Days())
}

// PullPreview is a dry run's result.
type PullPreview struct {
	Summary Summary
	Overlap Overlap
}

// Preview fetches the range into memory and summarises it exactly as a file
// preview does, without storing anything.
func (s *PullService) Preview(ctx context.Context, siteID int32, orgID int64, request PullRequest) (PullPreview, error) {
	rng, err := s.validate(ctx, orgID, &request)
	if err != nil {
		return PullPreview{}, err
	}
	if rng.Days() > MaxPreviewPullDays {
		return PullPreview{}, fmt.Errorf("%w: a preview covers at most %d days; import longer ranges directly", ErrPullInvalid, MaxPreviewPullDays)
	}
	puller, err := s.newPuller(ctx, request, request.Secret, orgID)
	if err != nil {
		return PullPreview{}, err
	}
	aggregate := newAggregate()
	var stats ParseStats
	for _, chunk := range ChunkRange(rng, puller.ChunkDays()) {
		part, partStats, err := puller.Fetch(ctx, chunk)
		if err != nil {
			return PullPreview{}, err
		}
		mergeInto(aggregate, clip(part, chunk))
		stats.Rows += partStats.Rows
		stats.BadRows += partStats.BadRows
		stats.DuplicateRows += partStats.DuplicateRows
		for _, rowError := range partStats.Errors {
			if len(stats.Errors) < maxRecordedErrors {
				stats.Errors = append(stats.Errors, rowError)
			}
		}
	}
	overlap, err := s.store.Preview(ctx, siteID, [32]byte{}, aggregate)
	if err != nil {
		return PullPreview{}, err
	}
	return PullPreview{Summary: Summarize(aggregate, stats), Overlap: overlap}, nil
}

func (s *PullService) newPuller(ctx context.Context, request PullRequest, secret string, orgID int64) (Puller, error) {
	switch request.Method {
	case MethodPlausibleAPI:
		return &plausiblePuller{client: s.client, base: request.BaseURL, siteID: request.RemoteSiteID, key: secret, pageSize: s.pageSize}, nil
	case MethodMatomoAPI:
		return &matomoPuller{client: s.client, base: request.BaseURL, siteID: request.RemoteSiteID, token: secret}, nil
	case MethodGA4API:
		if !s.ga4.Configured() {
			return nil, ErrGA4NotConfigured
		}
		refresh, err := s.ga4RefreshToken(ctx, orgID, request.ConnectionID)
		if err != nil {
			return nil, err
		}
		access := &ga4AccessToken{now: s.now, refresh: func(ctx context.Context) (string, time.Duration, error) {
			return s.ga4Refresh(ctx, refresh)
		}}
		return &ga4Puller{client: s.client, cfg: s.ga4, property: request.Property, token: access}, nil
	}
	return nil, fmt.Errorf("%w: unknown method", ErrPullInvalid)
}

// RunPull executes (or resumes) a pull import to completion. It is called by
// the Manager worker after the import was marked running. Context
// cancellation returns without changing the import's state, so the next
// start resumes from the stored cursor.
func (s *PullService) RunPull(ctx context.Context, id int64) error {
	pull, err := s.store.loadPull(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var secret string
	if len(pull.Sealed) > 0 {
		plain, err := s.box.Open(pull.Sealed)
		if err != nil {
			return errors.New("the stored credentials can no longer be decrypted; start the import again")
		}
		secret = string(plain)
	}
	puller, err := s.newPuller(ctx, pull.Request, secret, pull.OrgID)
	if err != nil {
		return err
	}
	for _, chunk := range ChunkRange(pull.Range, puller.ChunkDays()) {
		if pull.Cursor != nil && !chunk.To.After(*pull.Cursor) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		aggregate, stats, err := puller.Fetch(ctx, chunk)
		if err != nil {
			return err
		}
		if err := s.store.AppendPullChunk(ctx, id, chunk, clip(aggregate, chunk), stats); err != nil {
			if errors.Is(err, ErrNotFound) {
				slog.Info("pull import was removed while running", "import_id", id)
				return nil
			}
			return err
		}
	}
	if err := s.store.FinishPull(ctx, id); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// Recover resets interrupted pulls to pending and returns their ids.
func (s *PullService) Recover(ctx context.Context) ([]int64, error) {
	return s.store.RecoverInterruptedPulls(ctx)
}
