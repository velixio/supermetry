// Package exportschedule runs scheduled site exports to S3-compatible storage.
package exportschedule

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"supermetry/internal/safehttp"
)

// Destination is where exports are written.
type Destination struct {
	Endpoint    string // https://s3.eu-west-1.amazonaws.com, no path
	Region      string
	Bucket      string
	Prefix      string
	AccessKeyID string
	Secret      string
	PathStyle   bool
}

// ErrInvalidDestination marks a destination the caller must fix.
var ErrInvalidDestination = errors.New("invalid export destination")

// ValidateEndpoint checks the endpoint URL shape. Private and loopback
// addresses and plain HTTP are rejected unless allowPrivate (the operator
// opt-in SUPERMETRY_ALLOW_PRIVATE_EXPORT_ENDPOINTS) is set; the dialer
// enforces the same rule again for resolved addresses at connect time.
func ValidateEndpoint(raw string, allowPrivate bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("%w: endpoint must be an http(s) URL", ErrInvalidDestination)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("%w: endpoint must be a bare scheme://host[:port]", ErrInvalidDestination)
	}
	if !allowPrivate {
		if u.Scheme != "https" {
			return nil, fmt.Errorf("%w: endpoint must use https", ErrInvalidDestination)
		}
		host := strings.ToLower(u.Hostname())
		if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".internal") {
			return nil, fmt.Errorf("%w: private endpoints are not allowed on this instance", ErrInvalidDestination)
		}
		if ip, err := netip.ParseAddr(host); err == nil && safehttp.BlockedAddress(ip) {
			return nil, fmt.Errorf("%w: private endpoints are not allowed on this instance", ErrInvalidDestination)
		}
	}
	return u, nil
}

func (d Destination) validate(allowPrivate bool) (*url.URL, error) {
	u, err := ValidateEndpoint(d.Endpoint, allowPrivate)
	if err != nil {
		return nil, err
	}
	if d.Bucket == "" || d.AccessKeyID == "" || d.Secret == "" || d.Region == "" {
		return nil, fmt.Errorf("%w: region, bucket, access key id and secret are required", ErrInvalidDestination)
	}
	return u, nil
}

// S3 uploads exports. Its transport dials only through safehttp.GuardedDial.
type S3 struct {
	client   *minio.Client
	dest     Destination
	partSize uint64
}

// S3Options tune a client; the zero value is production behaviour.
type S3Options struct {
	AllowPrivate bool
	Resolver     safehttp.Resolver
	PartSize     uint64      // default 16 MiB; minio enforces its own minimum of 5 MiB
	TLSConfig    *tls.Config // tests
}

const defaultPartSize = 16 << 20

// NewS3 builds a client for the destination.
func NewS3(dest Destination, opt S3Options) (*S3, error) {
	u, err := dest.validate(opt.AllowPrivate)
	if err != nil {
		return nil, err
	}
	resolver := opt.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           safehttp.GuardedDial(resolver, dialer.DialContext, opt.AllowPrivate),
		TLSClientConfig:       opt.TLSConfig,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 2 * time.Minute,
		IdleConnTimeout:       30 * time.Second,
	}
	lookup := minio.BucketLookupAuto
	if dest.PathStyle {
		lookup = minio.BucketLookupPath
	}
	client, err := minio.New(u.Host, &minio.Options{
		Creds:        credentials.NewStaticV4(dest.AccessKeyID, dest.Secret, ""),
		Secure:       u.Scheme == "https",
		Region:       dest.Region,
		BucketLookup: lookup,
		Transport:    transport,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDestination, err)
	}
	part := opt.PartSize
	if part == 0 {
		part = defaultPartSize
	}
	return &S3{client: client, dest: dest, partSize: part}, nil
}

// Key returns the object key under the destination prefix.
func (s *S3) Key(name string) string {
	prefix := strings.Trim(s.dest.Prefix, "/")
	if prefix == "" {
		return name
	}
	return prefix + "/" + name
}

// Put streams r to the object. The size is unknown, so minio uploads in
// multipart parts of partSize (at most one part is buffered per upload
// thread, one here); objects smaller than a part use a single PUT.
func (s *S3) Put(ctx context.Context, key string, r io.Reader) (int64, error) {
	info, err := s.client.PutObject(ctx, s.dest.Bucket, key, r, -1, minio.PutObjectOptions{
		ContentType: "application/zip", PartSize: s.partSize, NumThreads: 1,
	})
	if err != nil {
		return 0, fmt.Errorf("upload %s: %w", key, err)
	}
	return info.Size, nil
}

// Check verifies credentials and bucket access by writing and deleting a tiny object.
func (s *S3) Check(ctx context.Context) error {
	key := s.Key(".supermetry-export-check")
	if _, err := s.client.PutObject(ctx, s.dest.Bucket, key, strings.NewReader("ok"), 2, minio.PutObjectOptions{}); err != nil {
		return fmt.Errorf("write test object: %w", err)
	}
	_ = s.client.RemoveObject(ctx, s.dest.Bucket, key, minio.RemoveObjectOptions{})
	return nil
}
