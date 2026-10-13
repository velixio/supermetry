package updatehelper

import (
	"strings"
	"testing"
)

func TestParseDeploymentDescriptor(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"binary", "docker"} {
		t.Run(mode, func(t *testing.T) {
			input := `{"version":1,"mode":"` + mode + `","service_name":"supermetry.service","versions_dir":"/opt/supermetry/versions","current_link":"/opt/supermetry/current","compose_dir":"/opt/supermetry","compose_service":"supermetry","backup_dir":"/var/lib/supermetry-updater/backups","readiness_url":"http://127.0.0.1:8080/readyz","socket_group":"supermetry-updater"}`
			got, err := ParseDeploymentDescriptor(strings.NewReader(input))
			if err != nil {
				t.Fatalf("ParseDeploymentDescriptor: %v", err)
			}
			if got.Mode != DeploymentMode(mode) {
				t.Fatalf("mode = %q, want %q", got.Mode, mode)
			}
		})
	}
}

func TestParseDeploymentDescriptorRejectsMutableOrUnsafeFacts(t *testing.T) {
	t.Parallel()

	valid := `{"version":1,"mode":"binary","service_name":"supermetry.service","versions_dir":"/opt/supermetry/versions","current_link":"/opt/supermetry/current","compose_dir":"/opt/supermetry","compose_service":"supermetry","backup_dir":"/var/lib/supermetry-updater/backups","readiness_url":"http://127.0.0.1:8080/readyz","socket_group":"supermetry-updater"}`
	tests := []struct {
		name string
		old  string
		new  string
	}{
		{name: "unknown field", old: `"socket_group":"supermetry-updater"`, new: `"socket_group":"supermetry-updater","command":"sh"`},
		{name: "service", old: `"service_name":"supermetry.service"`, new: `"service_name":"other.service"`},
		{name: "versions dir", old: `"versions_dir":"/opt/supermetry/versions"`, new: `"versions_dir":"/tmp/versions"`},
		{name: "readiness host", old: `http://127.0.0.1:8080/readyz`, new: `http://example.com/readyz`},
		{name: "readiness path", old: `/readyz`, new: `/admin`},
		{name: "socket group", old: `"socket_group":"supermetry-updater"`, new: `"socket_group":"root"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseDeploymentDescriptor(strings.NewReader(strings.Replace(valid, tt.old, tt.new, 1)))
			if err == nil {
				t.Fatal("accepted unsafe descriptor")
			}
		})
	}
}
