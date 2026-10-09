package privacy_test

import (
	"fmt"
	"reflect"
	"testing"

	"panel/internal/privacy"
)

func TestEvaluateGates(t *testing.T) {
	t.Parallel()
	for _, mode := range []privacy.Mode{privacy.Anonymous, privacy.Identified} {
		for _, profiles := range []bool{false, true} {
			for _, instanceReplay := range []bool{false, true} {
				for _, projectReplay := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/profiles=%t/instanceReplay=%t/projectReplay=%t", mode, profiles, instanceReplay, projectReplay), func(t *testing.T) {
						instance := privacy.Instance{Mode: mode, ReplayAllowed: instanceReplay}
						project := privacy.Project{ProfilesEnabled: profiles, ReplayEnabled: projectReplay}
						got, err := privacy.Evaluate(instance, project)
						if err != nil {
							t.Fatal(err)
						}
						if got.Instance != instance || got.Project != project {
							t.Fatal("configured inputs lost")
						}
						wantMode := privacy.Anonymous
						if mode == privacy.Identified && profiles {
							wantMode = privacy.Identified
						}
						if got.Effective.Mode != wantMode || got.Effective.ReplayEnabled != (instanceReplay && projectReplay) {
							t.Fatalf("effective = %+v", got.Effective)
						}
						var want []privacy.Reason
						if mode == privacy.Anonymous {
							want = append(want, privacy.InstanceAnonymous)
						}
						if !profiles {
							want = append(want, privacy.ProjectProfilesDisabled)
						}
						if !instanceReplay {
							want = append(want, privacy.InstanceReplayDisabled)
						}
						if !projectReplay {
							want = append(want, privacy.ProjectReplayDisabled)
						}
						if !reflect.DeepEqual(got.Restrictions, want) {
							t.Fatalf("restrictions = %v, want %v", got.Restrictions, want)
						}
					})
				}
			}
		}
	}
}

func TestEvaluateZeroValue(t *testing.T) {
	t.Parallel()
	got, err := privacy.Evaluate(privacy.Instance{}, privacy.Project{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Effective != (privacy.Effective{}) || got.Effective.Mode != privacy.Anonymous {
		t.Fatalf("unsafe zero value: %+v", got)
	}
	if privacy.Anonymous.String() != "anonymous" || privacy.Identified.String() != "identified" || privacy.Mode(255).String() != "invalid" {
		t.Fatal("unexpected mode names")
	}
}

func TestEvaluateReplayRestrictions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                    string
		instance, project, want privacy.ReplayRules
		reasons                 []privacy.Reason
	}{
		{"instance caps", privacy.ReplayRules{Mask: true, SampleRate: 10, RetentionDays: 7, QuotaBytes: 100}, privacy.ReplayRules{SampleRate: 90, RetentionDays: 30, QuotaBytes: 1000}, privacy.ReplayRules{Mask: true, SampleRate: 10, RetentionDays: 7, QuotaBytes: 100}, []privacy.Reason{privacy.MaskingRequired, privacy.SampleRateCapped, privacy.RetentionCapped, privacy.QuotaCapped}},
		{"stricter project preserved", privacy.ReplayRules{SampleRate: 90, RetentionDays: 30, QuotaBytes: 1000}, privacy.ReplayRules{Mask: true, SampleRate: 10, RetentionDays: 7, QuotaBytes: 100}, privacy.ReplayRules{Mask: true, SampleRate: 10, RetentionDays: 7, QuotaBytes: 100}, nil},
		{"zero caps never unlimited", privacy.ReplayRules{}, privacy.ReplayRules{Mask: true, SampleRate: 100, RetentionDays: 365, QuotaBytes: 1000}, privacy.ReplayRules{Mask: true}, []privacy.Reason{privacy.SampleRateCapped, privacy.RetentionCapped, privacy.QuotaCapped}},
		{"equal values", privacy.ReplayRules{Mask: true, SampleRate: 50, RetentionDays: 14, QuotaBytes: 500}, privacy.ReplayRules{Mask: true, SampleRate: 50, RetentionDays: 14, QuotaBytes: 500}, privacy.ReplayRules{Mask: true, SampleRate: 50, RetentionDays: 14, QuotaBytes: 500}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := privacy.Instance{Mode: privacy.Identified, ReplayAllowed: true, Replay: tt.instance}
			project := privacy.Project{ProfilesEnabled: true, ReplayEnabled: true, Replay: tt.project}
			got, err := privacy.Evaluate(instance, project)
			if err != nil {
				t.Fatal(err)
			}
			if got.Instance != instance || got.Project != project {
				t.Fatal("configured values changed")
			}
			if got.Effective.Replay != tt.want || !reflect.DeepEqual(got.Restrictions, tt.reasons) {
				t.Fatalf("got %+v, want rules %+v and reasons %v", got, tt.want, tt.reasons)
			}
		})
	}
}

func TestEvaluateRejectsInvalidInputs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		rules privacy.ReplayRules
	}{
		{"negative sample", privacy.ReplayRules{SampleRate: -1}},
		{"sample over 100", privacy.ReplayRules{SampleRate: 101}},
		{"negative retention", privacy.ReplayRules{RetentionDays: -1}},
		{"retention over 365", privacy.ReplayRules{RetentionDays: 366}},
		{"negative quota", privacy.ReplayRules{QuotaBytes: -1}},
	}
	for _, tt := range tests {
		for _, source := range []string{"instance", "project"} {
			t.Run(source+"/"+tt.name, func(t *testing.T) {
				instance := privacy.Instance{Mode: privacy.Identified, ReplayAllowed: true}
				project := privacy.Project{ProfilesEnabled: true, ReplayEnabled: true}
				if source == "instance" {
					instance.Replay = tt.rules
				} else {
					project.Replay = tt.rules
				}
				got, err := privacy.Evaluate(instance, project)
				if err == nil || !reflect.DeepEqual(got, privacy.Decision{}) {
					t.Fatalf("got %+v, %v; want zero decision and error", got, err)
				}
			})
		}
	}
	got, err := privacy.Evaluate(privacy.Instance{Mode: privacy.Mode(255), ReplayAllowed: true}, privacy.Project{ProfilesEnabled: true, ReplayEnabled: true})
	if err == nil || !reflect.DeepEqual(got, privacy.Decision{}) {
		t.Fatalf("invalid mode: %+v, %v", got, err)
	}
}

func FuzzReplayRulesTighten(f *testing.F) {
	f.Add(true, false, false, uint8(90), uint8(10), uint8(50), uint16(30), uint16(7), uint16(14), uint64(1000), uint64(100), uint64(500))
	f.Fuzz(func(t *testing.T, am, bm, cm bool, as, bs, cs uint8, ar, br, cr uint16, aq, bq, cq uint64) {
		rules := func(mask bool, sample uint8, retention uint16, quota uint64) privacy.ReplayRules {
			return privacy.ReplayRules{Mask: mask, SampleRate: int(sample) % 101, RetentionDays: int(retention) % 366, QuotaBytes: int64(quota >> 1)}
		}
		a, b, c := rules(am, as, ar, aq), rules(bm, bs, br, bq), rules(cm, cs, cr, cq)
		got := a.Tighten(b)
		if got.Mask != (a.Mask || b.Mask) || got.SampleRate > a.SampleRate || got.SampleRate > b.SampleRate || got.RetentionDays > a.RetentionDays || got.RetentionDays > b.RetentionDays || got.QuotaBytes > a.QuotaBytes || got.QuotaBytes > b.QuotaBytes {
			t.Fatalf("weakened %+v and %+v: %+v", a, b, got)
		}
		if got != b.Tighten(a) || a.Tighten(a) != a || got.Tighten(c) != a.Tighten(b.Tighten(c)) {
			t.Fatal("combination must be commutative, idempotent and associative")
		}
	})
}
