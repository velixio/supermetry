package release_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"supermetry/internal/updater/release"
)

func TestClientFetchArtifactUsesSignedSizeAndDigest(t *testing.T) {
	t.Parallel()
	payload := []byte("verified release artifact")
	digest := sha256.Sum256(payload)
	artifact := release.Artifact{
		Name: "supermetry_1.2.0_linux_amd64.tar.gz", OS: "linux", Arch: "amd64",
		Size: int64(len(payload)), SHA256: fmt.Sprintf("%x", digest),
	}
	client := release.NewClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://github.com/velixio/supermetry/releases/download/v1.2.0/"+artifact.Name {
			t.Fatalf("artifact URL = %q", request.URL)
		}
		return response(http.StatusOK, payload), nil
	})})
	var destination bytes.Buffer
	if err := client.FetchArtifact(context.Background(), "v1.2.0", artifact, &destination); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(destination.Bytes(), payload) {
		t.Fatalf("artifact = %q", destination.Bytes())
	}

	artifact.Size--
	if err := client.FetchArtifact(context.Background(), "v1.2.0", artifact, io.Discard); err == nil || !strings.Contains(err.Error(), "size mismatch") {
		t.Fatalf("size mismatch error = %v", err)
	}
}

func TestClientFetchManifestUsesOnlyOfficialBoundedEndpoints(t *testing.T) {
	t.Parallel()
	manifest := []byte(`{"format_version":1}` + "\n")
	signature := []byte(`{"key_id":"release","signature":"value"}` + "\n")
	var mu sync.Mutex
	var requested []string
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if _, ok := request.Context().Deadline(); !ok {
			t.Error("request context has no deadline")
		}
		if request.Method != http.MethodGet || request.Header.Get("User-Agent") != "supermetry-updater" {
			t.Errorf("request method/header = %s/%q", request.Method, request.Header.Get("User-Agent"))
		}
		mu.Lock()
		requested = append(requested, request.URL.String())
		mu.Unlock()
		body := manifest
		if strings.HasSuffix(request.URL.Path, ".sig") {
			body = signature
		}
		return response(http.StatusOK, body), nil
	})}

	manifestResult, signatureResult, err := release.NewClient(httpClient).FetchManifest(context.Background(), "v1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if string(manifestResult) != string(manifest) || string(signatureResult) != string(signature) {
		t.Fatalf("results = %q / %q", manifestResult, signatureResult)
	}
	want := []string{
		"https://github.com/velixio/supermetry/releases/download/v1.2.0/release-manifest.json",
		"https://github.com/velixio/supermetry/releases/download/v1.2.0/release-manifest.json.sig",
	}
	if len(requested) != len(want) || requested[0] != want[0] || requested[1] != want[1] {
		t.Fatalf("requested = %v, want %v", requested, want)
	}
}

func TestClientFetchManifestRejectsUnsafeVersionBeforeRequest(t *testing.T) {
	t.Parallel()
	client := release.NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("request made for unsafe version")
		return nil, nil
	})})
	for _, version := range []string{"latest", "v1.2.0-rc.1", "../v1.2.0", "1.2.0"} {
		if _, _, err := client.FetchManifest(context.Background(), version); err == nil {
			t.Fatalf("FetchManifest(%q) succeeded", version)
		}
	}
}

func TestClientFetchManifestRejectsRedirectStatusAndOversizedBodies(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		body   []byte
		want   string
	}{
		{name: "redirect", status: http.StatusFound, want: "untrusted"},
		{name: "manifest too large", status: http.StatusOK, body: bytesOf('m', release.MaxManifestBytes+1), want: "too large"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			client := release.NewClient(&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return nil }, Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				requests++
				result := response(tt.status, tt.body)
				result.Request = request
				if tt.status >= 300 && tt.status < 400 {
					result.Header.Set("Location", "https://supermetry.com/untrusted")
				}
				return result, nil
			})})
			_, _, err := client.FetchManifest(context.Background(), "v1.2.0")
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tt.want) {
				t.Fatalf("FetchManifest() error = %v, want %q", err, tt.want)
			}
			if requests != 1 {
				t.Fatalf("requests = %d, want 1", requests)
			}
		})
	}
}

func TestClientFetchManifestBoundsDetachedSignature(t *testing.T) {
	t.Parallel()
	client := release.NewClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, ".sig") {
			return response(http.StatusOK, bytesOf('s', (8<<10)+1)), nil
		}
		return response(http.StatusOK, []byte("{}\n")), nil
	})})
	_, _, err := client.FetchManifest(context.Background(), "v1.2.0")
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "too large") {
		t.Fatalf("FetchManifest() error = %v, want signature size error", err)
	}
}

func TestClientFetchManifestHonorsCancellation(t *testing.T) {
	t.Parallel()
	client := release.NewClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := client.FetchManifest(ctx, "v1.2.0")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("FetchManifest() error = %v, want context.Canceled", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func response(status int, body []byte) *http.Response {
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}
}

func bytesOf(value byte, count int64) []byte {
	return []byte(strings.Repeat(string(value), int(count)))
}
