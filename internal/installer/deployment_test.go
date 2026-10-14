package installer

import (
	"strings"
	"testing"

	"supermetry/internal/updatehelper"
)

func TestRenderDeploymentDescriptor(t *testing.T) {
	t.Parallel()

	for _, mode := range []updatehelper.DeploymentMode{updatehelper.DeploymentBinary, updatehelper.DeploymentDocker} {
		t.Run(string(mode), func(t *testing.T) {
			data, err := renderDeploymentDescriptor(mode, "127.0.0.1:8787")
			if err != nil {
				t.Fatal(err)
			}
			descriptor, err := updatehelper.ParseDeploymentDescriptor(strings.NewReader(string(data)))
			if err != nil {
				t.Fatalf("generated descriptor is invalid: %v\n%s", err, data)
			}
			if descriptor.Mode != mode || descriptor.ReadinessURL != "http://127.0.0.1:8787/readyz" {
				t.Fatalf("descriptor = %+v", descriptor)
			}
		})
	}
}
