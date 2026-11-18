package symsource

import (
	"path/filepath"
	"testing"
)

// The server passes <data dir>/symcache-tmp, which does not exist on a fresh
// install (found by the live Electron run): the fetcher creates it.
func TestFetchCreatesMissingTmpDir(t *testing.T) {
	r := newRig(t, nil, func(c *ElectronConfig) { c.TmpDir = filepath.Join(t.TempDir(), "data", "symcache-tmp") })
	if key, found := r.lookup(t, image(testElectron, testID)); !found || key != testCacheKey {
		t.Fatalf("lookup = %q, %v", key, found)
	}
}
