package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"time"

	"supermetry/internal/db"
	platformauth "supermetry/internal/platform/auth"
)

var placeholderRE = regexp.MustCompile(`\{[^}]+\}`)

type runOptions struct {
	Mode         string
	DSN          string
	Inventory    string
	Requests     string
	Output       string
	GeneratorBin string
	ServerBin    string
	Seed         uint64
	N            int
	PProf        bool
	Explain      int
}

func main() {
	if err := runCLI(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "api-bench:", err)
		os.Exit(1)
	}
}

func runCLI(args []string) error {
	if len(args) > 0 && args[0] == "compare" {
		return compareCLI(args[1:])
	}
	flags := flag.NewFlagSet("api-bench", flag.ContinueOnError)
	options := runOptions{}
	flags.StringVar(&options.Mode, "mode", "quick", "quick or full")
	flags.StringVar(&options.DSN, "dsn", os.Getenv("SUPERMETRY_BENCH_DSN"), "benchmark database URL")
	flags.StringVar(&options.Inventory, "inventory", "docs/api/inventory.json", "route inventory")
	flags.StringVar(&options.Requests, "requests", "tools/bench/api/requests.yaml", "request sets")
	flags.StringVar(&options.Output, "output", ".codex-runs/api-bench/latest", "output directory")
	flags.StringVar(&options.GeneratorBin, "generator-bin", ".codex-runs/api-bench/bin/gen", "generator binary")
	flags.StringVar(&options.ServerBin, "server-bin", ".codex-runs/api-bench/bin/supermetry", "server binary")
	flags.Uint64Var(&options.Seed, "seed", 42, "dataset seed")
	flags.IntVar(&options.N, "n", 0, "warm requests per concurrency")
	flags.BoolVar(&options.PProf, "pprof", false, "write CPU and allocation profiles")
	flags.IntVar(&options.Explain, "explain", 0, "EXPLAIN the N slowest captured SELECT queries")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if options.Mode != "quick" && options.Mode != "full" {
		return errors.New("--mode must be quick or full")
	}
	if options.DSN == "" {
		return errors.New("--dsn or SUPERMETRY_BENCH_DSN is required")
	}
	if options.N == 0 {
		if options.Mode == "full" {
			options.N = 200
		} else {
			options.N = 50
		}
	}
	return runBenchmark(context.Background(), options)
}

func runBenchmark(ctx context.Context, options runOptions) error {
	if err := os.MkdirAll(options.Output, 0o755); err != nil {
		return err
	}
	if err := resetBenchmarkDatabase(ctx, options.DSN); err != nil {
		return err
	}
	pool, err := openPool(ctx, options.DSN)
	if err != nil {
		return err
	}
	if err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		return fmt.Errorf("migrate benchmark database: %w", err)
	}
	tier := tierConfig(options.Mode)
	eventsPath, sitesPath, err := generateDataset(ctx, tier, options.Seed, filepath.Join(options.Output, "dataset"), options.GeneratorBin)
	if err != nil {
		pool.Close()
		return err
	}
	pool.Close()
	if err := loadDataset(ctx, options.DSN, sitesPath, eventsPath, options.ServerBin); err != nil {
		return err
	}
	pool, err = openPool(ctx, options.DSN)
	if err != nil {
		return err
	}
	defer pool.Close()
	userID, orgID, siteList, err := seedPlatform(ctx, pool, sitesPath, tier.Sites)
	if err != nil {
		return err
	}
	if err := closeDatasetDays(ctx, pool, siteList, benchmarkNow); err != nil {
		return err
	}
	environment, err := newBenchmarkEnv(ctx, pool, userID, orgID)
	if err != nil {
		return err
	}
	defer environment.Close()

	inventoryFile, err := os.Open(options.Inventory)
	if err != nil {
		return err
	}
	inventory, err := loadInventory(inventoryFile)
	inventoryFile.Close()
	if err != nil {
		return err
	}
	requestFile, err := os.Open(options.Requests)
	if err != nil {
		return err
	}
	sets, err := loadRequestSets(requestFile, inventory)
	requestFile.Close()
	if err != nil {
		return err
	}

	var cpuFile *os.File
	if options.PProf {
		cpuFile, err = os.Create(filepath.Join(options.Output, "cpu.pprof"))
		if err != nil {
			return err
		}
		if err := pprof.StartCPUProfile(cpuFile); err != nil {
			cpuFile.Close()
			return err
		}
		defer func() { pprof.StopCPUProfile(); _ = cpuFile.Close() }()
	}

	server := httptest.NewServer(environment.Handler)
	defer server.Close()
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{MaxIdleConns: 32, MaxIdleConnsPerHost: 16, IdleConnTimeout: 90 * time.Second}}
	var results []Result
	for _, set := range sets {
		pool.Reset()
		cold, err := executeOne(ctx, client, server.URL, set, environment)
		if err != nil {
			cold.err = err
		}
		for _, concurrency := range []int{1, 8} {
			result := measureSet(ctx, client, server.URL, set, environment, tier.Name, options.N, concurrency)
			result.ColdMS = float64(cold.duration) / float64(time.Millisecond)
			results = append(results, result)
		}
	}

	var postgresVersion string
	if err := pool.QueryRow(ctx, `select version()`).Scan(&postgresVersion); err != nil {
		return err
	}
	report := Report{
		Commit: commandOutput("git", "rev-parse", "--short", "HEAD"), GoVersion: runtime.Version(),
		PostgresVersion: postgresVersion, Machine: fmt.Sprintf("%s/%s %d CPU", runtime.GOOS, runtime.GOARCH, runtime.NumCPU()),
		Tier: tier.Name, Seed: options.Seed, GeneratedAt: time.Now().UTC().Format(time.RFC3339), Results: results,
	}
	jsonFile, err := os.Create(filepath.Join(options.Output, "result.json"))
	if err != nil {
		return err
	}
	if err := writeJSON(jsonFile, report); err != nil {
		jsonFile.Close()
		return err
	}
	if err := jsonFile.Close(); err != nil {
		return err
	}
	markdownFile, err := os.Create(filepath.Join(options.Output, "report.md"))
	if err != nil {
		return err
	}
	if err := writeMarkdown(markdownFile, report); err != nil {
		markdownFile.Close()
		return err
	}
	if err := markdownFile.Close(); err != nil {
		return err
	}
	if options.PProf {
		heapFile, err := os.Create(filepath.Join(options.Output, "allocs.pprof"))
		if err != nil {
			return err
		}
		runtime.GC()
		if err := pprof.WriteHeapProfile(heapFile); err != nil {
			heapFile.Close()
			return err
		}
		if err := heapFile.Close(); err != nil {
			return err
		}
	}
	if options.Explain > 0 {
		if err := explainCaptured(ctx, pool, options.Explain, filepath.Join(options.Output, "explain")); err != nil {
			return err
		}
	}
	fmt.Printf("benchmarked %d request sets (%d inventory routes); report: %s\n", len(sets), countInventoryRoutes(inventory), filepath.Join(options.Output, "report.md"))
	return nil
}

type sample struct {
	duration time.Duration
	bytes    int64
	status   int
	queries  float64
	dbMS     float64
	err      error
}

func executeOne(ctx context.Context, client *http.Client, baseURL string, set RequestSet, environment *benchmarkEnv) (sample, error) {
	path := resolvePath(set.Route, environment)
	values := url.Values{}
	for key, value := range set.Params {
		values.Set(key, fmt.Sprint(value))
	}
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var body io.Reader
	if set.Body != nil {
		encoded, err := json.Marshal(set.Body)
		if err != nil {
			return sample{}, err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, set.Method, baseURL+path, body)
	if err != nil {
		return sample{}, err
	}
	if set.Body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range set.Headers {
		request.Header.Set(key, value)
	}
	auth := set.Auth
	if set.Name == "default" && set.Method != http.MethodGet && set.Method != http.MethodHead && set.Method != http.MethodOptions {
		auth = "none"
	}
	switch auth {
	case "session":
		request.Header.Set("Cookie", platformauth.CookieName+"="+environment.Session+"; supermetry_csrf=benchmark-csrf")
		request.Header.Set("X-CSRF-Token", "benchmark-csrf")
	case "public-key":
		request.Header.Set("Authorization", "Bearer "+environment.APIKey)
	case "instance-admin":
		request.Header.Set("Authorization", "Bearer benchmark-instance-admin")
	}
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return sample{duration: time.Since(started), err: err}, err
	}
	defer response.Body.Close()
	read, readErr := io.Copy(io.Discard, response.Body)
	result := sample{duration: time.Since(started), bytes: read, status: response.StatusCode}
	result.queries, _ = strconv.ParseFloat(response.Header.Get("X-Bench-Queries"), 64)
	result.dbMS, _ = strconv.ParseFloat(response.Header.Get("X-Bench-DB-Ms"), 64)
	if readErr != nil {
		result.err = readErr
		return result, readErr
	}
	return result, nil
}

func resolvePath(pattern string, environment *benchmarkEnv) string {
	replacer := strings.NewReplacer(
		"{site}", strconv.Itoa(int(environment.SiteID)), "{org}", strconv.FormatInt(environment.OrgID, 10),
		"{key}", "plan", "{replay_path...}", "missing", "{hash}", "missing",
	)
	return placeholderRE.ReplaceAllString(replacer.Replace(pattern), "1")
}

func measureSet(ctx context.Context, client *http.Client, baseURL string, set RequestSet, environment *benchmarkEnv, dataset string, n, concurrency int) Result {
	started := time.Now()
	before := new(runtime.MemStats)
	runtime.ReadMemStats(before)
	samples := make([]sample, n)
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range concurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				measured, _ := executeOne(ctx, client, baseURL, set, environment)
				samples[index] = measured
			}
		}()
	}
	for index := range n {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	elapsed := time.Since(started)
	after := new(runtime.MemStats)
	runtime.ReadMemStats(after)
	durations := make([]time.Duration, 0, n)
	var bytesRead, errorsCount int64
	var status int
	var queryTotal, dbTotal float64
	for _, measured := range samples {
		durations = append(durations, measured.duration)
		bytesRead += measured.bytes
		queryTotal += measured.queries
		dbTotal += measured.dbMS
		status = measured.status
		if measured.err != nil || measured.status >= 500 {
			errorsCount++
		}
	}
	return Result{
		Route: set.Route, Method: set.Method, Request: set.Name, Params: set.Params, Dataset: dataset, Concurrency: concurrency,
		P50MS: durationMS(percentile(durations, .50)), P95MS: durationMS(percentile(durations, .95)), P99MS: durationMS(percentile(durations, .99)),
		RPS: float64(n) / max(elapsed.Seconds(), .000001), Bytes: bytesRead / int64(max(n, 1)), Status: status,
		DBQueries: queryTotal / float64(max(n, 1)), DBMS: dbTotal / float64(max(n, 1)),
		Allocs: float64(after.Mallocs-before.Mallocs) / float64(max(n, 1)), Errors: errorsCount,
	}
}

func durationMS(value time.Duration) float64 { return float64(value) / float64(time.Millisecond) }

func commandOutput(name string, args ...string) string {
	output, err := exec.Command(name, args...).Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(output))
}

func countInventoryRoutes(routes []InventoryRoute) int {
	count := 0
	for _, route := range routes {
		if route.Method != "" {
			count++
		}
	}
	return count
}

func compareCLI(args []string) error {
	flags := flag.NewFlagSet("api-bench compare", flag.ContinueOnError)
	baselinePath := flags.String("baseline", "", "baseline result.json")
	currentPath := flags.String("current", "", "current result.json")
	tolerance := flags.Float64("tolerance", .25, "allowed p95 fractional regression")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *baselinePath == "" || *currentPath == "" {
		return errors.New("compare requires --baseline and --current")
	}
	baseline, err := readReport(*baselinePath)
	if err != nil {
		return err
	}
	current, err := readReport(*currentPath)
	if err != nil {
		return err
	}
	regressions := compareResults(baseline.Results, current.Results, *tolerance)
	for _, regression := range regressions {
		if regression.Missing {
			fmt.Printf("MISSING %s\n", regression.Key)
		} else {
			fmt.Printf("REGRESSION %s %.2fms -> %.2fms (%+.1f%%)\n", regression.Key, regression.BaselineMS, regression.CurrentMS, regression.Change*100)
		}
	}
	if len(regressions) > 0 {
		return fmt.Errorf("%d benchmark regressions exceed %.1f%%", len(regressions), *tolerance*100)
	}
	fmt.Printf("no p95 regressions above %.1f%%\n", *tolerance*100)
	return nil
}

func readReport(path string) (Report, error) {
	file, err := os.Open(path)
	if err != nil {
		return Report{}, err
	}
	defer file.Close()
	var report Report
	if err := json.NewDecoder(file).Decode(&report); err != nil {
		return Report{}, err
	}
	return report, nil
}
