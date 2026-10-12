package imports

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/maphash"
	"io"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Limits for event-level exports (Mixpanel, Amplitude). They are variables so
// tests can lower them. The duplicate detector keeps one 8 byte hash per row.
var (
	maxEventRows      = 5_000_000
	maxEventSessions  = 2_000_000
	maxEventLineBytes = 1 << 20
)

const maxEventValueRunes = 512

// eventProvider selects the field names and conventions of one export format.
type eventProvider int

const (
	providerMixpanel eventProvider = iota
	providerAmplitude
)

// importedEvent is one validated export row reduced to what the importer
// keeps. Identifiers are only hashed for in-memory session grouping.
type importedEvent struct {
	at          time.Time
	user        uint64
	fingerprint uint64
	name        string
	pageview    bool
	ignored     bool
	path        string
	source      string
	country     string
	region      string
	city        string
	browser     string
	os          string
	device      string
}

type eventSessionKey struct {
	day  time.Time
	user uint64
}

// eventSession groups one visitor's events on one local day: one visit.
type eventSession struct {
	start, end          time.Time
	firstPage, lastPage time.Time
	entry, exit         string
	pageviews           int64
	source, country     string
	region, city        string
	browser, os         string
	device              string
	paths               map[string]int64
	events              map[string]int64
}

type eventAggregator struct {
	location *time.Location
	seed     maphash.Seed
	seen     map[uint64]struct{}
	sessions map[eventSessionKey]*eventSession
	rows     int
}

func newEventAggregator(location *time.Location) *eventAggregator {
	if location == nil {
		location = time.UTC
	}
	return &eventAggregator{
		location: location, seed: maphash.MakeSeed(),
		seen: make(map[uint64]struct{}), sessions: make(map[eventSessionKey]*eventSession),
	}
}

// add folds one event into its visitor-day session. It reports false for an
// exact duplicate, which the caller counts instead of importing.
func (a *eventAggregator) add(event importedEvent) (bool, error) {
	if _, dup := a.seen[event.fingerprint]; dup {
		return false, nil
	}
	a.seen[event.fingerprint] = struct{}{}
	local := event.at.In(a.location)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	key := eventSessionKey{day, event.user}
	session := a.sessions[key]
	if session == nil {
		if len(a.sessions) >= maxEventSessions {
			return false, fatalError{fmt.Errorf("import has more than %d sessions", maxEventSessions)}
		}
		session = &eventSession{start: event.at, end: event.at, paths: make(map[string]int64), events: make(map[string]int64)}
		a.sessions[key] = session
	}
	if event.at.Before(session.start) {
		session.start = event.at
	}
	if event.at.After(session.end) {
		session.end = event.at
	}
	session.source = firstNonempty(session.source, event.source)
	session.country = firstNonempty(session.country, event.country)
	session.region = firstNonempty(session.region, event.region)
	session.city = firstNonempty(session.city, event.city)
	session.browser = firstNonempty(session.browser, event.browser)
	session.os = firstNonempty(session.os, event.os)
	session.device = firstNonempty(session.device, event.device)
	if event.pageview {
		session.pageviews++
		if event.path != "" {
			session.paths[event.path]++
			if session.firstPage.IsZero() || event.at.Before(session.firstPage) {
				session.firstPage, session.entry = event.at, event.path
			}
			if session.lastPage.IsZero() || event.at.After(session.lastPage) {
				session.lastPage, session.exit = event.at, event.path
			}
		}
	} else {
		session.events[event.name]++
	}
	return true, nil
}

func (a *eventAggregator) finish() (Aggregate, error) {
	result := newAggregate()
	for key, session := range a.sessions {
		metrics := Metrics{Visitors: 1, Visits: 1, Pageviews: session.pageviews, VisitDurationS: int64(session.end.Sub(session.start) / time.Second)}
		if session.pageviews == 1 {
			metrics.Bounces = 1
		}
		result.addDay(key.day, metrics)
		for dim, value := range map[int16]string{
			DimSource: session.source, DimCountry: session.country, DimRegion: session.region, DimCity: session.city,
			DimBrowser: session.browser, DimOS: session.os, DimDevice: session.device,
			DimEntry: session.entry, DimExit: session.exit,
		} {
			result.addDimension(DimensionKey{key.day, dim, value}, metrics)
		}
		for path, views := range session.paths {
			result.addDimension(DimensionKey{key.day, DimPage, path}, Metrics{Visitors: 1, Visits: 1, Pageviews: views})
		}
		for name, count := range session.events {
			result.addDimension(DimensionKey{key.day, DimEvent, name}, Metrics{Visitors: 1, Visits: 1, Events: count})
		}
		if err := result.checkLimit(); err != nil {
			return result, err
		}
	}
	return result, nil
}

// parseEventFile reads a Mixpanel or Amplitude export. The upload may be a
// plain JSON-lines or CSV file, a gzip, or a ZIP holding any number of those
// (Amplitude exports a ZIP of hourly gzip files).
func parseEventFile(ctx context.Context, path string, location *time.Location, provider eventProvider) (Aggregate, ParseStats, error) {
	var stats ParseStats
	file, err := os.Open(path)
	if err != nil {
		return newAggregate(), stats, err
	}
	defer file.Close()
	agg := newEventAggregator(location)
	remaining := maxZIPTotalBytes
	var magic [4]byte
	n, _ := file.ReadAt(magic[:], 0)
	if n >= 4 && bytes.Equal(magic[:4], []byte("PK\x03\x04")) || n >= 4 && bytes.Equal(magic[:4], []byte("PK\x05\x06")) {
		info, err := file.Stat()
		if err != nil {
			return newAggregate(), stats, err
		}
		err = parseEventZIP(ctx, file, info.Size(), agg, &stats, &remaining, provider)
		if err != nil {
			return newAggregate(), stats, err
		}
	} else if err := parseEventStream(ctx, contextReader{ctx, file}, "", agg, &stats, &remaining, provider); err != nil {
		return newAggregate(), stats, err
	}
	if stats.Rows+stats.BadRows+stats.DuplicateRows == 0 {
		return newAggregate(), stats, errors.New("no events found in file")
	}
	result, err := agg.finish()
	return result, stats, err
}

func parseEventZIP(ctx context.Context, file io.ReaderAt, size int64, agg *eventAggregator, stats *ParseStats, remaining *uint64, provider eventProvider) error {
	archive, err := zip.NewReader(file, size)
	if err != nil {
		return fmt.Errorf("open ZIP: %w", err)
	}
	if len(archive.File) > maxZIPEntries {
		return fmt.Errorf("ZIP has %d entries, limit is %d", len(archive.File), maxZIPEntries)
	}
	var declared uint64
	var used int
	for _, entry := range archive.File {
		if entry.FileInfo().IsDir() || !eventEntryName(entry.Name) {
			continue
		}
		if entry.UncompressedSize64 > maxZIPEntryBytes {
			return fmt.Errorf("%s exceeds uncompressed size limit", entry.Name)
		}
		declared += entry.UncompressedSize64
		if declared > maxZIPTotalBytes {
			return fmt.Errorf("ZIP exceeds total uncompressed size limit of %d bytes", maxZIPTotalBytes)
		}
		reader, err := entry.Open()
		if err != nil {
			return fmt.Errorf("open %s: %w", entry.Name, err)
		}
		err = parseEventStream(ctx, contextReader{ctx, reader}, entryBase(entry.Name), agg, stats, remaining, provider)
		closeErr := reader.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return fmt.Errorf("close %s: %w", entry.Name, closeErr)
		}
		used++
	}
	if used == 0 {
		return errors.New("ZIP holds no .json, .jsonl, .csv or .gz event files")
	}
	return nil
}

// eventEntryName selects ZIP entries that can hold events and skips archive
// metadata such as macOS resource forks.
func eventEntryName(name string) bool {
	if strings.HasPrefix(name, "__MACOSX/") {
		return false
	}
	base := strings.ToLower(entryBase(name))
	if strings.HasPrefix(base, ".") {
		return false
	}
	for _, suffix := range []string{".json", ".jsonl", ".ndjson", ".csv", ".gz"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

// budgetReader fails once one stream or the whole upload inflates past the
// uncompressed limits, however the compressed form declared its size.
type budgetReader struct {
	r         io.Reader
	entryLeft uint64
	total     *uint64
}

func (b *budgetReader) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if uint64(n) > b.entryLeft {
		return 0, errors.New("file exceeds uncompressed size limit")
	}
	if uint64(n) > *b.total {
		return 0, fmt.Errorf("upload exceeds total uncompressed size limit of %d bytes", maxZIPTotalBytes)
	}
	b.entryLeft -= uint64(n)
	*b.total -= uint64(n)
	return n, err
}

// parseEventStream reads one logical file: it unwraps gzip, then picks JSON
// lines or CSV from the first significant byte. name prefixes row errors when
// the file came out of an archive.
func parseEventStream(ctx context.Context, input io.Reader, name string, agg *eventAggregator, stats *ParseStats, remaining *uint64, provider eventProvider) error {
	buffered := bufio.NewReaderSize(input, 64<<10)
	if head, _ := buffered.Peek(2); len(head) == 2 && head[0] == 0x1f && head[1] == 0x8b {
		unzipped, err := gzip.NewReader(buffered)
		if err != nil {
			return fmt.Errorf("%sopen gzip: %w", prefixName(name), err)
		}
		defer unzipped.Close()
		buffered = bufio.NewReaderSize(contextReader{ctx, unzipped}, 64<<10)
	}
	limited := &budgetReader{r: buffered, entryLeft: maxZIPEntryBytes, total: remaining}
	body := bufio.NewReaderSize(limited, 64<<10)

	first, err := firstSignificantByte(body)
	if err != nil {
		return fmt.Errorf("%sread: %w", prefixName(name), err)
	}
	handle := func(line int, rec map[string]string) error {
		err := handleEventRecord(rec, agg, stats, provider)
		if err != nil && name != "" && !errors.Is(err, errSkipRow) {
			var fatal fatalError
			if !errors.As(err, &fatal) {
				err = fmt.Errorf("%s: %w", name, err)
			}
		}
		return err
	}
	switch first {
	case 0:
		return nil
	case '[':
		return fmt.Errorf("%sJSON arrays are not supported: export JSON lines (one event per line)", prefixName(name))
	case '{':
		return eachJSONLine(body, stats, handle)
	default:
		return eachEventCSVRow(body, stats, handle)
	}
}

func prefixName(name string) string {
	if name == "" {
		return ""
	}
	return name + ": "
}

// firstSignificantByte skips whitespace and a byte order mark and returns the
// next byte without consuming it, or 0 for an empty stream.
func firstSignificantByte(r *bufio.Reader) (byte, error) {
	for {
		b, err := r.ReadByte()
		if errors.Is(err, io.EOF) {
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		case 0xEF:
			if head, _ := r.Peek(2); len(head) == 2 && head[0] == 0xBB && head[1] == 0xBF {
				_, _ = r.Discard(2)
				continue
			}
		}
		return b, r.UnreadByte()
	}
}

// finishRow applies the shared row accounting used by the CSV table: bad rows
// are recorded, skips pass silently, fatal errors end the parse.
func finishRow(stats *ParseStats, line int, err error) error {
	if err == nil {
		stats.Rows++
		return nil
	}
	if errors.Is(err, errSkipRow) {
		return nil
	}
	var fatal fatalError
	if errors.As(err, &fatal) {
		return fatal.err
	}
	stats.badRow(line, err)
	return nil
}

var ignoredEventKeys = map[string]bool{
	"ip": true, "$ip": true, "ip_address": true, "$user_agent": true, "user_agent": true, "$ua": true,
}

func eachJSONLine(r *bufio.Reader, stats *ParseStats, fn func(int, map[string]string) error) error {
	line := 0
	var pending []byte
	for {
		overflow := false
		chunk, err := r.ReadSlice('\n')
		// A line longer than the reader's buffer arrives in pieces; stop
		// storing once it passes the cap but keep consuming it.
		for errors.Is(err, bufio.ErrBufferFull) {
			if len(pending)+len(chunk) <= maxEventLineBytes {
				pending = append(pending, chunk...)
			} else {
				overflow = true
			}
			chunk, err = r.ReadSlice('\n')
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		eof := err != nil
		if len(pending)+len(chunk) > maxEventLineBytes {
			overflow = true
		}
		if len(chunk) > 0 || len(pending) > 0 || overflow {
			line++
			data := chunk
			if len(pending) > 0 && !overflow {
				pending = append(pending, chunk...)
				data = pending
			}
			if overflow {
				err = finishRow(stats, line, fmt.Errorf("line is longer than %d bytes", maxEventLineBytes))
			} else if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 {
				err = finishRow(stats, line, parseJSONEventLine(trimmed, line, fn))
			} else {
				err = nil
			}
			if err != nil {
				return err
			}
		}
		pending = pending[:0]
		if eof {
			return nil
		}
	}
}

func parseJSONEventLine(data []byte, line int, fn func(int, map[string]string) error) error {
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		return errors.New("invalid JSON")
	}
	rec := make(map[string]string, len(object)+8)
	// Nested property bags first, so top-level fields win name clashes.
	for _, bag := range []string{"properties", "event_properties"} {
		if nested, ok := object[bag].(map[string]any); ok {
			flattenScalars(rec, nested)
		}
	}
	flattenScalars(rec, object)
	return fn(line, rec)
}

func flattenScalars(into map[string]string, from map[string]any) {
	for key, value := range from {
		key = strings.ToLower(key)
		if ignoredEventKeys[key] {
			continue
		}
		if text, ok := scalarText(value); ok {
			into[key] = text
		}
	}
}

func scalarText(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return truncateRunes(strings.TrimSpace(v), 4*maxEventValueRunes), true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(v), true
	}
	return "", false
}

func eachEventCSVRow(r io.Reader, stats *ParseStats, fn func(int, map[string]string) error) error {
	table, err := newCSVTable(r, stats)
	if err != nil {
		return err
	}
	if len(table.header) == 0 {
		return errors.New("CSV file has no header")
	}
	names := make(map[string]int, len(table.header))
	for label, index := range table.header {
		label = strings.TrimPrefix(strings.TrimPrefix(label, "properties."), "event_properties.")
		if !ignoredEventKeys[label] {
			names[label] = index
		}
	}
	if !hasAnyKey(names, eventNameKeys) {
		return errors.New("CSV file needs an event column")
	}
	return table.each(func(row csvRow) error {
		rec := make(map[string]string, len(names))
		for label, index := range names {
			if index < len(row.values) {
				rec[label] = truncateRunes(strings.TrimSpace(row.values[index]), 4*maxEventValueRunes)
			}
		}
		return fn(row.line, rec)
	})
}

func hasAnyKey(m map[string]int, keys []string) bool {
	for _, key := range keys {
		if _, ok := m[key]; ok {
			return true
		}
	}
	return false
}

func lookup(rec map[string]string, keys []string) string {
	for _, key := range keys {
		if value := rec[key]; value != "" {
			return value
		}
	}
	return ""
}

var (
	eventNameKeys = []string{"event", "event_type", "event name", "event_name"}
	eventTimeKeys = []string{"time", "event_time", "timestamp", "$time"}
	pathKeys      = []string{
		"[amplitude] page path", "$pathname", "pathname", "path", "page path",
		"$current_url", "current_url", "[amplitude] page url", "page url", "page_url", "$url", "url",
	}
	referrerKeys = []string{
		"$referring_domain", "referring_domain", "[amplitude] referring domain",
		"$referrer", "referrer", "[amplitude] referrer", "utm_source",
	}
	countryKeys = []string{"mp_country_code", "country_code", "$country", "country"}
	regionKeys  = []string{"$region", "region"}
	cityKeys    = []string{"$city", "city"}
)

// pageviewEvents are the event names both products use for a page view.
var pageviewEvents = map[string]bool{
	"$mp_web_page_view": true, "mp_page_view": true, "$pageview": true, "pageview": true,
	"page view": true, "page_view": true, "viewed page": true, "[amplitude] page viewed": true,
	"page viewed": true,
}

// ignoredEvents carry identity or session bookkeeping, not visitor activity.
var ignoredEvents = map[string]bool{
	"$identify": true, "$create_alias": true, "$merge": true, "$disable_alias": true,
	"$groupidentify": true, "session_start": true, "session_end": true,
	"[amplitude] start session": true, "[amplitude] end session": true,
}

// handleEventRecord validates one row, then adds it to the aggregate.
func handleEventRecord(rec map[string]string, agg *eventAggregator, stats *ParseStats, provider eventProvider) error {
	agg.rows++
	if agg.rows > maxEventRows {
		return fatalError{fmt.Errorf("import has more than %d rows", maxEventRows)}
	}
	event, err := extractEvent(rec, agg, provider)
	if err != nil {
		return err
	}
	if event.ignored {
		return nil
	}
	added, err := agg.add(event)
	if err != nil {
		return err
	}
	if !added {
		stats.DuplicateRows++
		return errSkipRow
	}
	return nil
}

func extractEvent(rec map[string]string, agg *eventAggregator, provider eventProvider) (importedEvent, error) {
	var event importedEvent
	event.name = truncateRunes(lookup(rec, eventNameKeys), 200)
	if event.name == "" {
		return event, errors.New("missing event name")
	}
	timeText := lookup(rec, eventTimeKeys)
	if timeText == "" {
		timeKey := "time"
		if provider == providerAmplitude {
			timeKey = "event_time"
		}
		return event, fmt.Errorf("missing %s", timeKey)
	}
	at, err := parseEventTime(timeText)
	if err != nil {
		timeKey := "time"
		if provider == providerAmplitude {
			timeKey = "event_time"
		}
		return event, fmt.Errorf("invalid %s %q", timeKey, truncateRunes(timeText, 40))
	}
	event.at = at

	var userKeys, insertKeys []string
	userLabel := "distinct_id"
	switch provider {
	case providerAmplitude:
		userKeys = []string{"user_id", "device_id", "amplitude_id"}
		insertKeys = []string{"$insert_id", "uuid"}
		userLabel = "user_id or device_id"
	default:
		userKeys = []string{"distinct_id", "$distinct_id", "$user_id", "user_id", "$device_id", "device_id"}
		insertKeys = []string{"$insert_id", "insert_id"}
	}
	user := lookup(rec, userKeys)
	if user == "" {
		return event, fmt.Errorf("missing %s", userLabel)
	}
	lowerName := strings.ToLower(event.name)
	if ignoredEvents[lowerName] {
		event.ignored = true
		return event, nil
	}
	event.pageview = pageviewEvents[lowerName]
	event.user = maphash.String(agg.seed, user)
	// Mixpanel de-duplicates on event, time, distinct_id and $insert_id;
	// Amplitude on $insert_id (falling back to the event uuid) per device.
	var fingerprint maphash.Hash
	fingerprint.SetSeed(agg.seed)
	for _, part := range []string{lowerName, at.Format(time.RFC3339), user, lookup(rec, insertKeys)} {
		fingerprint.WriteString(part)
		fingerprint.WriteByte(0x1f)
	}
	event.fingerprint = fingerprint.Sum64()

	event.path = cleanEventPath(lookup(rec, pathKeys))
	event.source = cleanReferrer(lookup(rec, referrerKeys))
	event.country = eventCountry(lookup(rec, countryKeys))
	event.region = truncateRunes(lookup(rec, regionKeys), maxEventValueRunes)
	event.city = truncateRunes(lookup(rec, cityKeys), maxEventValueRunes)
	switch provider {
	case providerAmplitude:
		amplitudeClient(rec, &event)
	default:
		event.browser = truncateRunes(lookup(rec, []string{"$browser", "browser"}), maxEventValueRunes)
		event.os = eventOS(lookup(rec, []string{"$os", "os"}))
	}
	return event, nil
}

// amplitudeClient maps Amplitude's platform fields. On the web os_name holds
// the browser and device_family the operating system; on mobile os_name is the
// operating system. Full user agent strings are never read.
func amplitudeClient(rec map[string]string, event *importedEvent) {
	osName := lookup(rec, []string{"os_name"})
	switch strings.ToLower(lookup(rec, []string{"platform"})) {
	case "web":
		event.browser = truncateRunes(osName, maxEventValueRunes)
		event.os = eventOS(lookup(rec, []string{"device_family"}))
	case "ios", "android":
		event.os = eventOS(osName)
		event.device = "Mobile"
	}
}

func eventOS(value string) string {
	switch strings.ToLower(value) {
	case "":
		return ""
	case "ios":
		return "iOS"
	case "macos", "mac os x", "mac":
		return "macOS"
	}
	return truncateRunes(value, maxEventValueRunes)
}

func eventCountry(value string) string {
	if value == "" {
		return ""
	}
	code := normalizeGA4Value(DimCountry, value)
	if len(code) != 2 {
		return ""
	}
	return code
}

// cleanEventPath reduces a URL or path to its path: the host, query string
// and fragment can carry tokens or identifiers and are dropped.
func cleanEventPath(value string) string {
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil {
			return ""
		}
		value = parsed.Path
	} else if cut := strings.IndexAny(value, "?#"); cut >= 0 {
		value = value[:cut]
	}
	if value == "" {
		return "/"
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	return truncateRunes(value, maxEventValueRunes)
}

func cleanReferrer(value string) string {
	switch strings.ToLower(value) {
	case "", "none", "(none)":
		return ""
	case "$direct", "direct", "(direct)":
		return "Direct / None"
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Hostname() == "" {
			return ""
		}
		value = parsed.Hostname()
	}
	return truncateRunes(strings.TrimPrefix(value, "www."), maxEventValueRunes)
}

func truncateRunes(value string, max int) string {
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	runes := []rune(value)
	return string(runes[:max])
}

// parseEventTime accepts Unix seconds or milliseconds (Mixpanel) and the
// timestamp layouts of both products. Times without an offset are UTC, as in
// Amplitude's export. Sub-second precision is dropped.
func parseEventTime(value string) (time.Time, error) {
	var parsed time.Time
	if number, err := strconv.ParseFloat(value, 64); err == nil {
		if math.IsNaN(number) || math.IsInf(number, 0) || number <= 0 {
			return time.Time{}, errors.New("invalid time")
		}
		if number > 1e11 {
			number /= 1000
		}
		parsed = time.Unix(int64(number), 0).UTC()
	} else {
		ok := false
		for _, layout := range []string{
			time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05.999999999", "2006-01-02",
		} {
			if t, err := time.Parse(layout, value); err == nil {
				parsed, ok = t, true
				break
			}
		}
		if !ok {
			return time.Time{}, errors.New("invalid time")
		}
	}
	parsed = parsed.Truncate(time.Second)
	if year := parsed.Year(); year < 2000 || year > 2100 {
		return time.Time{}, errors.New("time out of range")
	}
	return parsed, nil
}
