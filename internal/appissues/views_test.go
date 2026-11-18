package appissues_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"supermetry/internal/appissues"
)

func TestSavedViewVisibility(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	private, err := f.svc.CreateView(ctx, 8, f.bobActor, "Mine", "is:unresolved assigned:me", false)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := f.svc.CreateView(ctx, 8, f.bobActor, "Team", `release:"1.0" !level:info`, true)
	if err != nil {
		t.Fatal(err)
	}
	names := func(userID int64, site int32) []string {
		list, err := f.svc.Views(ctx, site, userID)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, v := range list {
			out = append(out, v.Name)
		}
		return out
	}
	if got := strings.Join(names(f.bob, 8), ","); got != "Mine,Team" {
		t.Errorf("owner sees %q", got)
	}
	if got := strings.Join(names(f.alice, 8), ","); got != "Team" {
		t.Errorf("another member sees %q, want only the shared view", got)
	}
	if got := names(f.alice, 7); len(got) != 0 {
		t.Errorf("views of another site leaked: %v", got)
	}
	// Another member cannot touch a private view: it does not exist for them.
	alice := appissues.Actor{UserID: f.alice}
	if err := f.svc.DeleteView(ctx, 8, private.ID, alice); !errors.Is(err, appissues.ErrNotFound) {
		t.Errorf("delete of someone's private view: %v", err)
	}
	// A non-admin cannot change a shared view of another member; an admin can.
	if _, err := f.svc.UpdateView(ctx, 8, shared.ID, alice, appissues.ViewChange{Name: s("x")}); !errors.Is(err, appissues.ErrForbidden) {
		t.Errorf("non-admin update of a shared view: %v", err)
	}
	if v, err := f.svc.UpdateView(ctx, 8, shared.ID, f.actor, appissues.ViewChange{Name: s("Renamed"), Shared: b(true)}); err != nil || v.Name != "Renamed" || v.Mine {
		t.Errorf("admin update: %+v, %v", v, err)
	}
	// The wrong site finds nothing.
	if err := f.svc.DeleteView(ctx, 7, private.ID, f.bobActor); !errors.Is(err, appissues.ErrNotFound) {
		t.Errorf("delete via another site: %v", err)
	}
	if err := f.svc.DeleteView(ctx, 8, private.ID, f.bobActor); err != nil {
		t.Errorf("owner delete: %v", err)
	}
}

func TestSavedViewValidation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for name, c := range map[string][2]string{
		"empty name":   {" ", "is:resolved"},
		"long name":    {strings.Repeat("n", 81), "is:resolved"},
		"unknown key":  {"v", "nope:1"},
		"long query":   {"v", strings.Repeat("a", 201)},
		"nul in query": {"v", "a\x00"},
		"nul in name":  {"a\x00", "a"},
		"bad operator": {"v", "events:lots"},
	} {
		if _, err := f.svc.CreateView(ctx, 7, f.actor, c[0], c[1], false); !errors.Is(err, appissues.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if _, err := f.svc.CreateView(ctx, 7, f.actor, "Same", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreateView(ctx, 7, f.actor, "Same", "is:resolved", false); !errors.Is(err, appissues.ErrViewExists) {
		t.Errorf("duplicate name: %v", err)
	}
}
