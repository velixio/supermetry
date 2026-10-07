package verify

import (
	"strings"
	"testing"
)

func TestLoadTruthGeneratorFormat(t *testing.T) {
	js := `{"generator":"panel/bench/gen","tier":"tiny","rows":[
{"site":1,"date":"2026-09-28","visitors":995,"visits":1196,"pageviews":2862,"bounces":684,"duration_sum_s":205634.691,
 "top":{"source":[{"value":"(direct)","visitors":10,"pageviews":12}]},"funnel_f":[5,2,1],"signup_plan":{"free":1,"pro":0,"team":2}}]}`
	rows, err := LoadTruth(strings.NewReader(js))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Site != 1 || rows[0].Visits != 1196 || rows[0].Top["source"][0].Value != DirectLabel {
		t.Fatalf("%+v", rows)
	}
	if ms, _ := durMs(rows[0].DurationSumS); ms != 205634691 {
		t.Fatalf("duration ms %d", ms)
	}
	if unlabel("source", DirectLabel) != "" || label("source", "") != DirectLabel || label("path", "") != "" {
		t.Fatal("direct label mapping")
	}
}
