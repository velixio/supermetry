package query

import (
	"fmt"
	"os"
	"testing"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func TestMain(m *testing.M) {
	testpg.EnableParallelReuse()
	if err := testpg.EnableTemplate(db.Migrate); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	testpg.CloseTemplate()
	os.Exit(code)
}
