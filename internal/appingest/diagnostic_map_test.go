package appingest

import (
	"strings"
	"testing"
	"time"
)

func voiceProps() map[string]string {
	return map[string]string{
		"category": "backend_crash", "source": "backend", "stage": "model_load",
		"error_code": "GPU_OOM", "error_type": "RuntimeError", "engine_id": "faster-whisper",
		"app_version": "1.2.3", "build_commit": "abcdef1", "platform": "linux", "arch": "x64",
		"device_family": "cuda", "exit_code": "137", "signal": "SIGKILL", "reason": "oom",
		"frames":      "/home/bob/app/backend/model_manager.py:12 load\nbackend/x.py:3 run",
		"fingerprint": strings.Repeat("ab", 32), "uptime_bucket": "lt_1h", "elapsed_bucket": "lt_1m",
		"memory_bucket": "lt_16gb", "disk_bucket": "lt_32gb", "occurrences": "3", "schema_version": "1",
	}
}

func TestMapDiagnosticFullReport(t *testing.T) {
	ts := testNow.Add(-72 * time.Hour).UnixMilli()
	e, err := MapDiagnostic(DiagnosticInput{ID: "evt-1", TimestampMS: &ts, ProfileID: "install_0123", Props: voiceProps()})
	if err != nil {
		t.Fatal(err)
	}
	if e.ID != "evt-1" || e.TS.UnixMilli() != ts || e.Kind != "crash" || e.Level != "fatal" || e.Handled {
		t.Fatalf("core: %+v", e)
	}
	if e.Release != "1.2.3" || e.Build != "abcdef1" || e.Platform != "linux" || e.Arch != "x64" || e.DeviceClass != "cuda" {
		t.Fatalf("environment fields: %+v", e)
	}
	if e.ErrorType != "RuntimeError" || e.ExitReason != "oom" || e.Signal != "SIGKILL" || e.ExitCode == nil || *e.ExitCode != 137 {
		t.Fatalf("crash fields: %+v", e)
	}
	if e.Fingerprint != strings.Repeat("ab", 32) || e.Occurrences != 3 || e.InstallID != "install_0123" {
		t.Fatalf("fingerprint/occurrences/install: %+v", e)
	}
	if e.Frames != "~/app/backend/model_manager.py:12 load\nbackend/x.py:3 run" {
		t.Fatalf("frames: %q", e.Frames)
	}
	for _, key := range []string{"category", "source", "stage", "error_code", "engine_id", "uptime_bucket", "elapsed_bucket", "memory_bucket", "disk_bucket", "schema_version"} {
		if e.Tags[key] == "" {
			t.Errorf("tag %s missing", key)
		}
	}
	if e.Tags["category"] != "backend_crash" || e.Tags["error_code"] != "GPU_OOM" {
		t.Fatalf("tags: %v", e.Tags)
	}
	for _, consumed := range []string{"app_version", "frames", "fingerprint", "occurrences", "reason", "signal", "exit_code", "device_family", "platform", "arch", "error_type", "build_commit"} {
		if _, ok := e.Tags[consumed]; ok {
			t.Errorf("%s must not be duplicated as a tag", consumed)
		}
	}
}

func TestMapDiagnosticCategories(t *testing.T) {
	cases := map[string]string{
		"app_crash": "crash", "backend_crash": "crash", "renderer_crash": "crash",
		"install_failed": "install_failed", "install_stalled": "install_stalled", "incompatibility": "incompatibility",
		"generation_failed": "operation_failed", "model_load_failed": "operation_failed", "engine_failed": "operation_failed",
		"download_failed": "operation_failed", "operation_failed": "operation_failed",
		"something_new": "error", "": "error",
	}
	for category, want := range cases {
		props := map[string]string{"category": category}
		e, err := MapDiagnostic(DiagnosticInput{ID: "x", Props: props})
		if err != nil {
			t.Fatalf("%q: %v", category, err)
		}
		if e.Kind != want {
			t.Errorf("category %q -> kind %q, want %q", category, e.Kind, want)
		}
		if category != "" && e.Tags["category"] != category {
			t.Errorf("category %q lost from tags: %v", category, e.Tags)
		}
		if (want == "crash") != (e.Level == "fatal") {
			t.Errorf("category %q level %q", category, e.Level)
		}
	}
}

func TestMapDiagnosticNormalisesPlatformAndArch(t *testing.T) {
	cases := []struct{ platform, arch, wantPlatform, wantArch string }{
		{"darwin", "arm64", "macos", "arm64"}, {"win32", "x86_64", "windows", "x64"}, {"Linux", "amd64", "linux", "x64"},
		{"freebsd", "mips", "other", "other"}, {"", "", "", ""}, {"android", "aarch64", "android", "arm64"},
	}
	for _, c := range cases {
		e, err := MapDiagnostic(DiagnosticInput{ID: "x", Props: map[string]string{"category": "app_crash", "platform": c.platform, "arch": c.arch}})
		if err != nil {
			t.Fatal(err)
		}
		if e.Platform != c.wantPlatform || e.Arch != c.wantArch {
			t.Errorf("%s/%s -> %s/%s, want %s/%s", c.platform, c.arch, e.Platform, e.Arch, c.wantPlatform, c.wantArch)
		}
	}
}

func TestMapDiagnosticToleratesBadNumbersAndGeneratesAnID(t *testing.T) {
	e, err := MapDiagnostic(DiagnosticInput{Props: map[string]string{"category": "app_crash", "exit_code": "n/a", "occurrences": "0"}})
	if err != nil {
		t.Fatal(err)
	}
	if e.ExitCode != nil || e.Occurrences != 1 || e.ID == "" {
		t.Fatalf("%+v", e)
	}
	other, _ := MapDiagnostic(DiagnosticInput{Props: map[string]string{"category": "app_crash"}})
	if other.ID == e.ID {
		t.Fatal("generated ids must differ")
	}
}

func TestMapDiagnosticLimitsTags(t *testing.T) {
	props := map[string]string{"category": "app_crash", "long": strings.Repeat("v", 500)}
	for i := range 30 {
		props["extra"+strings.Repeat("x", i)] = "v"
	}
	e, err := MapDiagnostic(DiagnosticInput{ID: "x", Props: props})
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Tags) > 20 {
		t.Fatalf("%d tags", len(e.Tags))
	}
	if v, ok := e.Tags["long"]; ok && len(v) > 200 {
		t.Fatalf("long tag value kept at %d bytes", len(v))
	}
	if e.Tags["category"] == "" {
		t.Fatal("the category tag must win over extras")
	}
}

func TestMapDiagnosticRejectsOverlongFields(t *testing.T) {
	props := map[string]string{"category": "app_crash", "app_version": strings.Repeat("1", 65)}
	if _, err := MapDiagnostic(DiagnosticInput{ID: "x", Props: props}); err == nil {
		t.Fatal("an over-long release must be invalid, as on the app endpoint")
	}
}
