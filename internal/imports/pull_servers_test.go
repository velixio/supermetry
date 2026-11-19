package imports

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The servers below replay hand-written JSON templates from testdata/pull.
// Each template row repeats on every requested day, so expected totals are
// the template numbers times the number of days (computed by hand in tests).

func loadTemplate(t *testing.T, name string, into any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "pull", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func daysBetween(from, to string) []string {
	start, _ := time.Parse(time.DateOnly, from)
	end, _ := time.Parse(time.DateOnly, to)
	var days []string
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		days = append(days, day.Format(time.DateOnly))
	}
	return days
}

func testPullClient() (*pullClient, *[]time.Duration) {
	var mu sync.Mutex
	sleeps := &[]time.Duration{}
	return newPullClient(PullHTTPOptions{AllowPrivate: true, Sleep: func(_ context.Context, d time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		*sleeps = append(*sleeps, d)
		return nil
	}}), sleeps
}

// ---- Plausible ----

type plausibleRecorded struct {
	Key          string
	From, To     string
	Offset, Size int
}

type plausibleFake struct {
	*httptest.Server
	mu       sync.Mutex
	template map[string][]struct {
		Value   any   `json:"value"`
		Metrics []any `json:"metrics"`
	}
	requests []plausibleRecorded
	// before can answer a request itself (429, 400...); true means handled.
	before func(w http.ResponseWriter, r plausibleRecorded, attempt int) bool
	calls  map[string]int
}

func newPlausibleFake(t *testing.T, apiKey string) *plausibleFake {
	t.Helper()
	fake := &plausibleFake{calls: map[string]int{}}
	loadTemplate(t, "plausible_template.json", &fake.template)
	fake.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/query" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+apiKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body struct {
			SiteID     string         `json:"site_id"`
			DateRange  []string       `json:"date_range"`
			Dimensions []string       `json:"dimensions"`
			Pagination map[string]int `json:"pagination"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		key := ""
		if len(body.Dimensions) > 1 {
			key = body.Dimensions[1]
		}
		recorded := plausibleRecorded{Key: key, From: body.DateRange[0], To: body.DateRange[1], Offset: body.Pagination["offset"], Size: body.Pagination["limit"]}
		fake.mu.Lock()
		fake.requests = append(fake.requests, recorded)
		fake.calls[key]++
		attempt := fake.calls[key]
		fake.mu.Unlock()
		if fake.before != nil && fake.before(w, recorded, attempt) {
			return
		}
		rows, ok := fake.template[key]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"unknown dimension"}`))
			return
		}
		var results []map[string]any
		for _, day := range daysBetween(body.DateRange[0], body.DateRange[1]) {
			for _, row := range rows {
				dims := []any{day}
				if row.Value != nil {
					dims = append(dims, row.Value)
				}
				results = append(results, map[string]any{"dimensions": dims, "metrics": row.Metrics})
			}
		}
		lo := min(body.Pagination["offset"], len(results))
		hi := min(lo+body.Pagination["limit"], len(results))
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results[lo:hi], "meta": map[string]any{}})
	}))
	t.Cleanup(fake.Close)
	return fake
}

func (f *plausibleFake) recorded() []plausibleRecorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]plausibleRecorded(nil), f.requests...)
}

// chunkStarts lists the distinct from-dates of the totals queries, in order.
func (f *plausibleFake) chunkStarts() []string {
	var starts []string
	for _, r := range f.recorded() {
		if r.Key == "" && r.Offset == 0 {
			starts = append(starts, r.From)
		}
	}
	return starts
}

// ---- Matomo ----

type matomoFake struct {
	*httptest.Server
	mu        sync.Mutex
	template  map[string]json.RawMessage
	methods   []string
	emptyDays map[string]bool
	fail      map[string]string // method -> error message answered as result=error
	formSeen  []string
	queries   []string
}

func newMatomoFake(t *testing.T, token string) *matomoFake {
	t.Helper()
	fake := &matomoFake{emptyDays: map[string]bool{}, fail: map[string]string{}}
	loadTemplate(t, "matomo_template.json", &fake.template)
	fake.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.php" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		fake.mu.Lock()
		fake.queries = append(fake.queries, r.URL.RawQuery)
		fake.mu.Unlock()
		if r.PostForm.Get("token_auth") != token {
			_, _ = w.Write([]byte(`{"result":"error","message":"You can't access this resource as it requires 'view' access"}`))
			return
		}
		method := r.PostForm.Get("method")
		fake.mu.Lock()
		fake.methods = append(fake.methods, method)
		fake.formSeen = append(fake.formSeen, r.PostForm.Encode())
		fake.mu.Unlock()
		if message, ok := fake.fail[method]; ok {
			_ = json.NewEncoder(w).Encode(map[string]string{"result": "error", "message": message})
			return
		}
		raw, ok := fake.template[method]
		if !ok {
			_ = json.NewEncoder(w).Encode(map[string]string{"result": "error", "message": "method does not exist"})
			return
		}
		if method == "Goals.getGoals" {
			_, _ = w.Write(raw)
			return
		}
		parts := strings.Split(r.PostForm.Get("date"), ",")
		out := map[string]json.RawMessage{}
		for _, day := range daysBetween(parts[0], parts[1]) {
			if fake.emptyDays[day] {
				out[day] = json.RawMessage(`[]`)
				continue
			}
			out[day] = raw
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(fake.Close)
	return fake
}

func (f *matomoFake) methodCount(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, method := range f.methods {
		if method == name {
			count++
		}
	}
	return count
}

// ---- Google ----

type googleFake struct {
	*httptest.Server
	mu       sync.Mutex
	template map[string][]struct {
		Dims    []string `json:"dims"`
		Metrics []string `json:"metrics"`
	}
	reports      []googleReport
	accessTokens int
	refreshToken string
	tokenForms   []map[string]string
	quotaHits    int // answer this many runReport calls with 429 first
	revoked      []string
}

type googleReport struct {
	Dimension string
	From, To  string
	Offset    int
	Limit     int
	Auth      string
}

func newGoogleFake(t *testing.T) *googleFake {
	t.Helper()
	fake := &googleFake{refreshToken: "1//refresh-secret-value"}
	loadTemplate(t, "ga4_template.json", &fake.template)
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form := map[string]string{}
		for key := range r.PostForm {
			form[key] = r.PostForm.Get(key)
		}
		fake.mu.Lock()
		fake.tokenForms = append(fake.tokenForms, form)
		fake.accessTokens++
		n := fake.accessTokens
		fake.mu.Unlock()
		reply := map[string]any{"access_token": "ya29.access-" + strconv.Itoa(n), "expires_in": 3600}
		if form["grant_type"] == "authorization_code" {
			if form["code_verifier"] == "" || form["code"] != "good-code" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			reply["refresh_token"] = fake.refreshToken
		} else if form["refresh_token"] != fake.refreshToken {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(reply)
	})
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		fake.mu.Lock()
		fake.revoked = append(fake.revoked, r.PostForm.Get("token"))
		fake.mu.Unlock()
	})
	mux.HandleFunc("/v1beta/accountSummaries", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ya29.") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("pageToken") == "" {
			_, _ = w.Write([]byte(`{"accountSummaries":[{"account":"accounts/1","displayName":"Main","propertySummaries":[{"property":"properties/123","displayName":"Shop"}]}],"nextPageToken":"p2"}`))
			return
		}
		_, _ = w.Write([]byte(`{"accountSummaries":[{"account":"accounts/2","displayName":"Blog","propertySummaries":[{"property":"properties/456","displayName":"Blog site"},{"property":"bad","displayName":"x"}]}]}`))
	})
	mux.HandleFunc("/v1beta/properties/123:runReport", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			DateRanges []struct{ StartDate, EndDate string } `json:"dateRanges"`
			Dimensions []struct{ Name string }               `json:"dimensions"`
			Limit      string                                `json:"limit"`
			Offset     string                                `json:"offset"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		key := ""
		if len(body.Dimensions) > 1 {
			key = body.Dimensions[1].Name
		}
		limit, _ := strconv.Atoi(body.Limit)
		offset, _ := strconv.Atoi(body.Offset)
		fake.mu.Lock()
		fake.reports = append(fake.reports, googleReport{Dimension: key, From: body.DateRanges[0].StartDate, To: body.DateRanges[0].EndDate, Offset: offset, Limit: limit, Auth: r.Header.Get("Authorization")})
		quota := fake.quotaHits > 0
		if quota {
			fake.quotaHits--
		}
		fake.mu.Unlock()
		if quota {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":429,"message":"quota","status":"RESOURCE_EXHAUSTED"}}`))
			return
		}
		rows, ok := fake.template[key]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"unknown dimension"}}`))
			return
		}
		var all []map[string]any
		for _, day := range daysBetween(body.DateRanges[0].StartDate, body.DateRanges[0].EndDate) {
			compact := strings.ReplaceAll(day, "-", "")
			for _, row := range rows {
				dims := []map[string]string{{"value": compact}}
				for _, value := range row.Dims {
					dims = append(dims, map[string]string{"value": value})
				}
				metrics := make([]map[string]string, len(row.Metrics))
				for i, value := range row.Metrics {
					metrics[i] = map[string]string{"value": value}
				}
				all = append(all, map[string]any{"dimensionValues": dims, "metricValues": metrics})
			}
		}
		lo := min(offset, len(all))
		hi := min(lo+limit, len(all))
		_ = json.NewEncoder(w).Encode(map[string]any{"rows": all[lo:hi], "rowCount": len(all)})
	})
	fake.Server = httptest.NewServer(mux)
	t.Cleanup(fake.Close)
	return fake
}

func (f *googleFake) config() GA4Config {
	return GA4Config{
		ClientID: "client-id", ClientSecret: "client-secret", BaseURL: "https://panel.example.test",
		AuthURL: f.URL + "/auth", TokenURL: f.URL + "/token", RevokeURL: f.URL + "/revoke",
		DataBase: f.URL, AdminBase: f.URL,
	}
}

func (f *googleFake) reportCount(dimension string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, report := range f.reports {
		if report.Dimension == dimension {
			count++
		}
	}
	return count
}

func sortedKeys[V any](values map[string]V) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return fmt.Sprint(keys)
}
