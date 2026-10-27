package replay

import (
	"reflect"
	"testing"

	"supermetry/internal/config"
	"supermetry/internal/sites"
)

func TestResolveSiteOrgRetentionCap(t *testing.T) {
	for _, tc := range []struct {
		name         string
		instance     *int // SUPERMETRY_REPLAY_RETENTION_DAYS
		org          *int
		project      *int
		wantDays     int
		wantMaximum  *int
		wantReason   string // value restriction
		wantOrgLabel bool   // org_retention_capped in the restrictions list
	}{
		{name: "null everywhere uses the default", wantDays: 30},
		{name: "project value alone", project: ptr(14), wantDays: 14},
		{name: "instance cap only", instance: ptr(20), wantDays: 20, wantMaximum: ptr(20), wantReason: "instance_retention_days_cap"},
		{name: "org cap only lowers the default", org: ptr(7), wantDays: 7, wantMaximum: ptr(7), wantReason: OrgRetentionCapped, wantOrgLabel: true},
		{name: "org cap above the default changes nothing", org: ptr(45), wantDays: 30, wantMaximum: ptr(45)},
		{name: "both, org lower", instance: ptr(60), org: ptr(45), project: ptr(90), wantDays: 45, wantMaximum: ptr(45), wantReason: OrgRetentionCapped, wantOrgLabel: true},
		{name: "both, instance lower", instance: ptr(20), org: ptr(45), project: ptr(90), wantDays: 20, wantMaximum: ptr(20), wantReason: "instance_retention_days_cap"},
		{name: "both equal, instance reported", instance: ptr(30), org: ptr(30), project: ptr(90), wantDays: 30, wantMaximum: ptr(30), wantReason: "instance_retention_days_cap"},
		{name: "project lower than both", instance: ptr(60), org: ptr(45), project: ptr(10), wantDays: 10, wantMaximum: ptr(45)},
		{name: "free plan 30 over a 365 project", org: ptr(30), project: ptr(365), wantDays: 30, wantMaximum: ptr(30), wantReason: OrgRetentionCapped, wantOrgLabel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveSite(config.ReplayEnv{Enabled: true, Retention: tc.instance},
				sites.ReplaySiteInput{Enabled: true, RetentionDays: tc.project, OrgRetentionCapDays: tc.org})
			r := got.RetentionDays
			if r.V != tc.wantDays || r.Restriction != tc.wantReason {
				t.Fatalf("retention = %d (%q), want %d (%q)", r.V, r.Restriction, tc.wantDays, tc.wantReason)
			}
			if (r.Maximum == nil) != (tc.wantMaximum == nil) || (r.Maximum != nil && *r.Maximum != *tc.wantMaximum) {
				t.Fatalf("maximum = %v, want %v", r.Maximum, tc.wantMaximum)
			}
			has := false
			for _, restriction := range got.Restrictions {
				has = has || restriction == OrgRetentionCapped
			}
			if has != tc.wantOrgLabel {
				t.Fatalf("restrictions = %v, org label want %v", got.Restrictions, tc.wantOrgLabel)
			}
		})
	}
}

// A nil org cap must leave the resolved settings exactly as they were before
// the cap existed, for every combination of the other inputs.
func TestResolveSiteNilOrgCapChangesNothing(t *testing.T) {
	for _, instance := range []*int{nil, ptr(7), ptr(365)} {
		for _, project := range []*int{nil, ptr(1), ptr(90)} {
			env := config.ReplayEnv{Enabled: true, Retention: instance}
			with := ResolveSite(env, sites.ReplaySiteInput{Enabled: true, RetentionDays: project})
			// The reference computation is the pre-existing resolver step itself.
			want := resolveCap(defaultRetentionDays, project, instance, "instance_retention_days_cap")
			if !reflect.DeepEqual(with.RetentionDays, want) {
				t.Fatalf("instance=%v project=%v: %+v, want %+v", instance, project, with.RetentionDays, want)
			}
			if len(with.Restrictions) != 0 {
				t.Fatalf("unexpected restrictions %v", with.Restrictions)
			}
		}
	}
}
