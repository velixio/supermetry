package updatehelper

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestFileStoreSaveLoadAndPermissions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := NewFileStore(dir)
	want := Job{ID: "42", TargetVersion: "v1.2.3", State: StatePreflight}
	if err := store.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := store.Load(want.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.ID != want.ID || got.TargetVersion != want.TargetVersion || got.State != want.State {
		t.Fatalf("job = %#v, want %#v", got, want)
	}
	info, err := os.Stat(filepath.Join(dir, want.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %#o, want 0600", got)
	}
}

func TestFileStoreCrashBoundariesRecoverOldOrNewCompleteState(t *testing.T) {
	t.Parallel()
	points := []PersistPoint{AfterTempWrite, AfterTempSync, AfterRename, AfterDirectorySync}
	for transition := 0; transition < len(orderedStates)-1; transition++ {
		oldState := orderedStates[transition]
		newState := orderedStates[transition+1]
		for _, point := range points {
			t.Run(fmt.Sprintf("%s_to_%s/%s", oldState, newState, point), func(t *testing.T) {
				dir := t.TempDir()
				store := NewFileStore(dir)
				old := Job{ID: "42", TargetVersion: "v1.2.3", State: oldState}
				if err := store.Save(old); err != nil {
					t.Fatal(err)
				}
				store.InjectFault = func(got PersistPoint) error {
					if got == point {
						return errors.New("simulated power loss")
					}
					return nil
				}
				newJob := old
				newJob.State = newState
				if err := store.Save(newJob); err == nil {
					t.Fatal("Save succeeded despite injected fault")
				}
				recovered, err := NewFileStore(dir).Load(old.ID)
				if err != nil {
					t.Fatalf("Load after fault: %v", err)
				}
				want := oldState
				if point == AfterRename || point == AfterDirectorySync {
					want = newState
				}
				if recovered.State != want {
					t.Fatalf("state = %q, want %q", recovered.State, want)
				}
			})
		}
	}
}

func TestFileStoreRejectsCorruptAndMismatchedJobs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "42.json"), []byte(`{"id":"43","target_version":"v1.2.3","state":"preflight"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileStore(dir).Load("42"); err == nil {
		t.Fatal("Load accepted mismatched job ID")
	}
}

func TestFileStoreRejectsSymlinkJob(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(target, []byte(`{"id":"42","target_version":"v1.2.3","state":"preflight"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "42.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileStore(dir).Load("42"); err == nil {
		t.Fatal("Load accepted symlink job state")
	}
}
