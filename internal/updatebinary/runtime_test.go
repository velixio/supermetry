package updatebinary

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"supermetry/internal/appbackup"
	"supermetry/internal/updatehelper"
)

func TestBinaryBackupCreatorUsesOldBinaryEnvironmentAndVerifiesArchive(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	output := filepath.Join(directory, "backup.tar.gz")
	runner := &backupRunner{content: []byte("verified backup")}
	creator := NewBinaryBackupCreator(runner)
	creator.Verify = func(_ context.Context, path string) (appbackup.Manifest, error) {
		if path != output {
			t.Fatalf("verify path = %q", path)
		}
		return appbackup.Manifest{CreatedAt: "2026-10-14T00:00:00Z", SupermetryVersion: "1.1.0", SchemaVersion: 149, PGDumpVersion: "pg_dump 17"}, nil
	}
	request := BackupRequest{
		Executable:  "/opt/supermetry/versions/v1.1.0/supermetry",
		Environment: []string{"SUPERMETRY_DATABASE_HOST=db"}, OutputPath: output, Version: "v1.1.0",
	}
	result, err := creator.CreateAndVerify(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != output || result.SchemaVersion != 149 || result.Size != int64(len(runner.content)) || result.SHA256 == "" {
		t.Fatalf("result = %#v", result)
	}
	if runner.executable != request.Executable || !reflect.DeepEqual(runner.args, []string{"backup", "--out", output}) || !reflect.DeepEqual(runner.options.Env, request.Environment) {
		t.Fatalf("runner call = %q %#v %#v", runner.executable, runner.args, runner.options)
	}
}

type backupRunner struct {
	content    []byte
	executable string
	args       []string
	options    updatehelper.RunOptions
}

func (r *backupRunner) Run(_ context.Context, executable string, args []string, options updatehelper.RunOptions) (updatehelper.RunResult, error) {
	r.executable = executable
	r.args = append([]string(nil), args...)
	r.options = updatehelper.RunOptions{Dir: options.Dir, Env: append([]string(nil), options.Env...)}
	output := args[len(args)-1]
	if err := os.WriteFile(output, r.content, 0o600); err != nil {
		return updatehelper.RunResult{}, err
	}
	return updatehelper.RunResult{}, nil
}

var _ updatehelper.Runner = (*backupRunner)(nil)
