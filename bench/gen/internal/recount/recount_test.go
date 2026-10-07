package recount

import (
	"fmt"
	"strings"
	"testing"
)

// line builds one CONTRACT event line.
func line(ts, path, ref, utm, name, props, ip, ua, country, sk string) string {
	if props == "" {
		props = "{}"
	}
	return fmt.Sprintf(`{"ts":%q,"site":1,"host":"site1.test","path":%q,"ref":%q,"utm_source":%q,"utm_medium":"","utm_campaign":"","name":%q,"props":%s,"ip":%q,"ua":%q,"screen_w":390,"country":%q,"browser":"Safari","os":"iOS","device":"mobile","vk":"v-%s","sk":%q}`+"\n",
		ts, path, ref, utm, name, props, ip, ua, country, ip, sk)
}

var testSites = []Site{{ID: 1, Host: "site1.test", Timezone: "Asia/Kolkata", Share: 1}}

// A hand-computed fixture. Site 1 is Asia/Kolkata (+05:30), so local
// 2026-07-03 is 2026-07-02T18:30Z .. 2026-07-03T18:30Z.
//
// Visitor A (1.1.1.1):
//
//	s1 18:40Z  /         ref google
//	   18:41Z  /pricing
//	   18:42Z  signup plan=pro
//	   18:50Z  /docs                       -> 3 pv, 1 custom, 600 s, funnel 3 steps
//	s2 20:00Z  /docs     utm newsletter    -> bounce, entry=exit=/docs
//
// Visitor B (2.2.2.2):
//
//	s3 19:00Z  /pricing  direct
//	   19:00:30Z download                  -> custom event: not a bounce
//	   19:05Z  /         (5 min later)     -> 2 pv, 300 s, exit /, funnel step 1 only
//
// Visitor A again at 2026-07-03T18:40Z is local 2026-07-04: a new day row.
func fixture() string {
	var b strings.Builder
	b.WriteString(line("2026-07-02T18:40:00.000Z", "/", "https://www.google.com/", "", "pageview", "", "1.1.1.1", "UA-A", "IN", "s1"))
	b.WriteString(line("2026-07-02T18:41:00.000Z", "/pricing", "https://site1.test/", "", "pageview", "", "1.1.1.1", "UA-A", "IN", "s1"))
	b.WriteString(line("2026-07-02T18:42:00.000Z", "/pricing", "https://site1.test/", "", "signup", `{"plan":"pro"}`, "1.1.1.1", "UA-A", "IN", "s1"))
	b.WriteString(line("2026-07-02T18:50:00.000Z", "/docs", "https://site1.test/pricing", "", "pageview", "", "1.1.1.1", "UA-A", "IN", "s1"))
	b.WriteString(line("2026-07-02T19:00:00.000Z", "/pricing", "", "", "pageview", "", "2.2.2.2", "UA-B", "US", "s3"))
	b.WriteString(line("2026-07-02T19:00:30.000Z", "/pricing", "", "", "download", `{"file":"x.pdf"}`, "2.2.2.2", "UA-B", "US", "s3"))
	b.WriteString(line("2026-07-02T19:05:00.000Z", "/", "https://site1.test/pricing", "", "pageview", "", "2.2.2.2", "UA-B", "US", "s3"))
	b.WriteString(line("2026-07-02T20:00:00.000Z", "/docs", "", "newsletter", "pageview", "", "1.1.1.1", "UA-A", "IN", "s2"))
	b.WriteString(line("2026-07-03T18:40:00.000Z", "/", "", "", "pageview", "", "1.1.1.1", "UA-A", "IN", "s4"))
	return b.String()
}

func TestRecountHandComputed(t *testing.T) {
	res, err := Run(strings.NewReader(fixture()), testSites, Options{CheckKeyOrderEvery: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Violations) != 0 {
		t.Fatalf("violations: %v", res.Violations)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(res.Rows))
	}
	r := res.Rows[0]
	if r.Date != "2026-07-03" || r.Site != 1 {
		t.Fatalf("row 0 is %d %s", r.Site, r.Date)
	}
	check := func(name string, got, want int64) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %d, want %d", name, got, want)
		}
	}
	check("visitors", r.Visitors, 2)
	check("visits", r.Visits, 3)
	check("pageviews", r.Pageviews, 6)
	check("bounces", r.Bounces, 1)
	check("duration_ms", r.DurationMs, 600_000+0+300_000)
	// s1 reaches all 3 steps; s3 has "/" only after "/pricing", so step 1 only.
	if r.Funnel != [3]int64{2, 1, 1} {
		t.Errorf("funnel = %v, want [2 1 1]", r.Funnel)
	}
	if r.Plan["pro"] != 1 || r.Plan["free"] != 0 || r.Plan["team"] != 0 {
		t.Errorf("plan = %v", r.Plan)
	}
	wantTop := map[string][]TopEntry{
		// path: / by A and B (2 visitors, 2 pv); /pricing by A and B (2, 2); /docs by A (1, 2)
		"path":    {{"/", 2, 2}, {"/pricing", 2, 2}, {"/docs", 1, 2}},
		"country": {{"IN", 1, 4}, {"US", 1, 2}},
		// source is per session: google (s1, 3 pv), (direct) (s3, 2 pv), newsletter (s2, 1 pv)
		"source":  {{"www.google.com", 1, 3}, {"(direct)", 1, 2}, {"newsletter", 1, 1}},
		"browser": {{"Safari", 2, 6}},
		"os":      {{"iOS", 2, 6}},
		"device":  {{"mobile", 2, 6}},
		// entry: / (s1, 3 pv), /pricing (s3, 2 pv), /docs (s2, 1 pv)
		"entry": {{"/", 1, 3}, {"/pricing", 1, 2}, {"/docs", 1, 1}},
		// exit: /docs (s1 and s2 of A: 1 visitor, 4 pv), / (s3, 2 pv)
		"exit": {{"/docs", 1, 4}, {"/", 1, 2}},
	}
	for _, d := range Dims {
		got := r.Top[d]
		want := wantTop[d]
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("top %s = %v, want %v", d, got, want)
		}
	}
	r2 := res.Rows[1]
	if r2.Date != "2026-07-04" || r2.Visitors != 1 || r2.Visits != 1 || r2.Bounces != 1 {
		t.Errorf("row 1 = %+v", r2)
	}
}

func TestRecountFlagsViolations(t *testing.T) {
	var b strings.Builder
	// Same sk with a 26-minute gap, then a new sk only 10 minutes later.
	b.WriteString(line("2026-07-02T19:00:00.000Z", "/", "", "", "pageview", "", "1.1.1.1", "UA", "IN", "s1"))
	b.WriteString(line("2026-07-02T19:26:00.000Z", "/", "", "", "pageview", "", "1.1.1.1", "UA", "IN", "s1"))
	b.WriteString(line("2026-07-02T19:36:00.000Z", "/", "", "", "pageview", "", "1.1.1.1", "UA", "IN", "s2"))
	// Out of order.
	b.WriteString(line("2026-07-02T19:35:00.000Z", "/", "", "", "pageview", "", "3.3.3.3", "UA", "IN", "s3"))
	// A session crossing local midnight (18:30Z).
	b.WriteString(line("2026-07-03T18:20:00.000Z", "/", "", "", "pageview", "", "4.4.4.4", "UA", "IN", "s4"))
	b.WriteString(line("2026-07-03T18:40:00.000Z", "/", "", "", "pageview", "", "4.4.4.4", "UA", "IN", "s4"))
	res, err := Run(strings.NewReader(b.String()), testSites, Options{})
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(res.Violations, "\n")
	for _, want := range []string{"intra-session gap", "inter-session gap", "goes backwards", "crosses site-local midnight"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing violation %q in:\n%s", want, all)
		}
	}
}

func TestLoadTruthAndCompare(t *testing.T) {
	truth := `{"tier":"x","rows":[
{"site":1,"date":"2026-07-03","visitors":2,"visits":3,"pageviews":6,"bounces":1,"duration_sum_s":900.000,"top":{"path":[{"value":"/","visitors":2,"pageviews":2},{"value":"/pricing","visitors":2,"pageviews":2},{"value":"/docs","visitors":1,"pageviews":2}],"country":[{"value":"IN","visitors":1,"pageviews":4},{"value":"US","visitors":1,"pageviews":2}],"source":[{"value":"www.google.com","visitors":1,"pageviews":3},{"value":"(direct)","visitors":1,"pageviews":2},{"value":"newsletter","visitors":1,"pageviews":1}],"browser":[{"value":"Safari","visitors":2,"pageviews":6}],"os":[{"value":"iOS","visitors":2,"pageviews":6}],"device":[{"value":"mobile","visitors":2,"pageviews":6}],"entry":[{"value":"/","visitors":1,"pageviews":3},{"value":"/pricing","visitors":1,"pageviews":2},{"value":"/docs","visitors":1,"pageviews":1}],"exit":[{"value":"/docs","visitors":1,"pageviews":4},{"value":"/","visitors":1,"pageviews":2}]},"funnel_f":[2,1,1],"signup_plan":{"free":0,"pro":1,"team":0}},
{"site":1,"date":"2026-07-04","visitors":1,"visits":1,"pageviews":1,"bounces":1,"duration_sum_s":0.000,"top":{"path":[{"value":"/","visitors":1,"pageviews":1}],"country":[{"value":"IN","visitors":1,"pageviews":1}],"source":[{"value":"(direct)","visitors":1,"pageviews":1}],"browser":[{"value":"Safari","visitors":1,"pageviews":1}],"os":[{"value":"iOS","visitors":1,"pageviews":1}],"device":[{"value":"mobile","visitors":1,"pageviews":1}],"entry":[{"value":"/","visitors":1,"pageviews":1}],"exit":[{"value":"/","visitors":1,"pageviews":1}]},"funnel_f":[1,0,0],"signup_plan":{"free":0,"pro":0,"team":0}}
],"events":9}`
	want, err := LoadTruth(strings.NewReader(truth))
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(strings.NewReader(fixture()), testSites, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if d := Compare(res.Rows, want, 10); len(d) != 0 {
		t.Fatalf("diffs: %v", d)
	}
	want[0].Bounces = 2
	if d := Compare(res.Rows, want, 10); len(d) != 1 {
		t.Fatalf("want 1 diff, got %v", d)
	}
}

func TestSecondsToMs(t *testing.T) {
	for in, want := range map[string]int64{"0": 0, "1.5": 1500, "12.345": 12345, "900.000": 900000} {
		got, err := secondsToMs(in)
		if err != nil || got != want {
			t.Errorf("secondsToMs(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	if _, err := secondsToMs("1.2345"); err == nil {
		t.Error("want error for 4 decimals")
	}
}
