package exportschedule

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
)

func testDest(f *fakeS3) Destination {
	return Destination{Endpoint: f.Server.URL, Region: "us-east-1", Bucket: "bucket", Prefix: "exports", AccessKeyID: "AKIDEXAMPLE", Secret: "secret", PathStyle: true}
}

func TestPutSingleAndMultipart(t *testing.T) {
	f := newFakeS3(t)
	c, err := NewS3(testDest(f), S3Options{AllowPrivate: true, TLSConfig: f.tlsConfig(), PartSize: 5 << 20})
	if err != nil {
		t.Fatal(err)
	}
	small := bytes.Repeat([]byte("a"), 1000)
	if n, err := c.Put(context.Background(), c.Key("small.zip"), bytes.NewReader(small)); err != nil || n != 1000 {
		t.Fatalf("small put = %d, %v", n, err)
	}
	got, ok := f.object("bucket/exports/small.zip")
	if !ok || !bytes.Equal(got, small) {
		t.Fatalf("small object stored=%v", ok)
	}
	uploads, parts := f.Multipart, f.Parts
	big := make([]byte, 12<<20+123)
	for i := range big {
		big[i] = byte(i * 7)
	}
	if n, err := c.Put(context.Background(), c.Key("big.zip"), bytes.NewReader(big)); err != nil || n != int64(len(big)) {
		t.Fatalf("big put = %d, %v", n, err)
	}
	got, _ = f.object("bucket/exports/big.zip")
	if !bytes.Equal(got, big) {
		t.Fatalf("big object differs (%d vs %d bytes)", len(got), len(big))
	}
	if f.Multipart-uploads != 1 || f.Parts-parts != 3 {
		t.Fatalf("multipart uploads=%d parts=%d, want 1 upload with 3 parts", f.Multipart-uploads, f.Parts-parts)
	}
}

func TestPutAbortsOnReaderError(t *testing.T) {
	f := newFakeS3(t)
	c, _ := NewS3(testDest(f), S3Options{AllowPrivate: true, TLSConfig: f.tlsConfig(), PartSize: 5 << 20})
	boom := errors.New("export failed")
	r := &failingReader{data: bytes.Repeat([]byte("z"), 6<<20), err: boom}
	if _, err := c.Put(context.Background(), "bucket-less/key.zip", r); err == nil {
		t.Fatal("want error")
	}
	if _, ok := f.object("bucket/bucket-less/key.zip"); ok {
		t.Fatal("partial object was stored")
	}
	if f.Aborted != 1 {
		t.Fatalf("aborted uploads = %d, want 1", f.Aborted)
	}
}

type failingReader struct {
	data []byte
	err  error
}

func (r *failingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func TestEndpointRejectsPrivateWithoutOptIn(t *testing.T) {
	for _, endpoint := range []string{"https://127.0.0.1:9000", "https://10.0.0.5", "https://localhost:9000", "http://s3.example.com", "https://169.254.169.254", "https://[::1]:9000", "https://user:pw@s3.example.com", "https://s3.example.com/path", "ftp://s3.example.com"} {
		if _, err := ValidateEndpoint(endpoint, false); !errors.Is(err, ErrInvalidDestination) {
			t.Errorf("ValidateEndpoint(%q) = %v, want ErrInvalidDestination", endpoint, err)
		}
	}
	for _, endpoint := range []string{"https://s3.eu-west-1.amazonaws.com", "https://abc.r2.cloudflarestorage.com"} {
		if _, err := ValidateEndpoint(endpoint, false); err != nil {
			t.Errorf("ValidateEndpoint(%q) = %v", endpoint, err)
		}
	}
	if _, err := ValidateEndpoint("http://127.0.0.1:9000", true); err != nil {
		t.Errorf("opt-in should allow a local MinIO: %v", err)
	}
}

type fixedResolver struct{ addr string }

func (r fixedResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr(r.addr)}, nil
}

// A hostname that resolves to a private address passes URL validation but the
// guarded dialer must refuse it, before any request reaches the server.
func TestDialGuardRejectsHostnameResolvingToPrivate(t *testing.T) {
	f := newFakeS3(t)
	_, port, _ := strings.Cut(strings.TrimPrefix(f.Server.URL, "https://"), ":")
	dest := testDest(f)
	dest.Endpoint = "https://s3.rebind.example.com:" + port
	c, err := NewS3(dest, S3Options{AllowPrivate: false, Resolver: fixedResolver{"127.0.0.1"}, TLSConfig: f.tlsConfig()})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "blocked address") {
		t.Fatalf("Check error = %v, want blocked address", err)
	}
	if f.Requests != 0 {
		t.Fatalf("server saw %d requests, want 0", f.Requests)
	}
}

func TestCheckWritesAndRemovesObject(t *testing.T) {
	f := newFakeS3(t)
	c, _ := NewS3(testDest(f), S3Options{AllowPrivate: true, TLSConfig: f.tlsConfig()})
	if err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.keysStored()) != 0 {
		t.Fatalf("test object left behind: %v", f.keysStored())
	}
	f.FailPut = true
	if err := c.Check(context.Background()); err == nil {
		t.Fatal("want access denied")
	}
}
