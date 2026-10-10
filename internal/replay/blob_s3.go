package replay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type S3Store struct {
	client *minio.Client
	bucket string
	prefix string
	spills *spillSet
}

func NewS3Store(cfg S3Settings, spillDir string) (*S3Store, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.Region == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("incomplete replay S3 settings")
	}
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return nil, errors.New("invalid replay S3 endpoint")
	}
	if endpoint.Path != "" && endpoint.Path != "/" {
		return nil, errors.New("replay S3 endpoint must not contain a path")
	}
	spills, err := newSpillSet(spillDir)
	if err != nil {
		return nil, err
	}
	client, err := minio.New(endpoint.Host, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure:       endpoint.Scheme == "https",
		Region:       cfg.Region,
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		return nil, fmt.Errorf("create replay S3 client: %w", err)
	}
	return &S3Store{
		client: client, bucket: cfg.Bucket,
		prefix: strings.Trim(cfg.Prefix, "/"), spills: spills,
	}, nil
}

func (s *S3Store) Append(ctx context.Context, key string, r io.Reader) (int64, error) {
	return s.spills.append(ctx, key, r)
}

func (s *S3Store) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if _, err := validateBlobPath(key); err != nil {
		return nil, err
	}
	if reader, ok, err := s.spills.open(key); err != nil || ok {
		return reader, err
	}
	objectKey := s.objectKey(key)
	if _, err := s.client.StatObject(ctx, s.bucket, objectKey, minio.StatObjectOptions{}); err != nil {
		if isS3Missing(err) {
			return nil, fmt.Errorf("open replay S3 object: %w", fs.ErrNotExist)
		}
		return nil, fmt.Errorf("stat replay S3 object: %w", err)
	}
	object, err := s.client.GetObject(ctx, s.bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("open replay S3 object: %w", err)
	}
	return object, nil
}

func (s *S3Store) PutIfAbsent(ctx context.Context, key string, r io.Reader) (bool, error) {
	if _, err := validateBlobPath(key); err != nil {
		return false, err
	}
	body, err := io.ReadAll(contextReader{ctx: ctx, reader: r})
	if err != nil {
		return false, fmt.Errorf("read replay S3 object: %w", err)
	}
	opts := minio.PutObjectOptions{DisableMultipart: true, DisableContentSha256: true}
	opts.SetMatchETagExcept("*")
	_, err = s.client.PutObject(ctx, s.bucket, s.objectKey(key), bytes.NewReader(body), int64(len(body)), opts)
	if err != nil {
		response := minio.ToErrorResponse(err)
		if response.StatusCode == 412 || response.Code == "PreconditionFailed" {
			return false, nil
		}
		return false, fmt.Errorf("put replay S3 object: %w", err)
	}
	return true, nil
}

func (s *S3Store) DeletePrefix(ctx context.Context, prefix string) (int64, error) {
	clean, err := validateBlobPath(prefix)
	if err != nil {
		return 0, err
	}
	prefix = strings.TrimSuffix(filepath.ToSlash(clean), "/") + "/"
	freed, err := s.spills.removePrefix(prefix)
	if err != nil {
		return freed, err
	}
	objectPrefix := s.objectKey(prefix)
	listCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	for object := range s.client.ListObjects(listCtx, s.bucket, minio.ListObjectsOptions{Prefix: objectPrefix, Recursive: true}) {
		if object.Err != nil {
			return freed, fmt.Errorf("list replay S3 objects: %w", object.Err)
		}
		if err := s.client.RemoveObject(ctx, s.bucket, object.Key, minio.RemoveObjectOptions{}); err != nil {
			return freed, fmt.Errorf("delete replay S3 object: %w", err)
		}
		freed += object.Size
	}
	return freed, nil
}

func (s *S3Store) Delete(ctx context.Context, key string) (int64, error) {
	if _, err := validateBlobPath(key); err != nil {
		return 0, err
	}
	freed, err := s.spills.removeKey(key)
	if err != nil {
		return 0, err
	}
	objectKey := s.objectKey(key)
	info, err := s.client.StatObject(ctx, s.bucket, objectKey, minio.StatObjectOptions{})
	if isS3Missing(err) {
		return freed, nil
	}
	if err != nil {
		return freed, fmt.Errorf("stat replay S3 object: %w", err)
	}
	if err := s.client.RemoveObject(ctx, s.bucket, objectKey, minio.RemoveObjectOptions{}); err != nil {
		return freed, fmt.Errorf("delete replay S3 object: %w", err)
	}
	return freed + info.Size, nil
}

func (s *S3Store) List(ctx context.Context, prefix string) ([]BlobInfo, error) {
	clean, err := validateBlobPath(prefix)
	if err != nil {
		return nil, err
	}
	prefix = strings.TrimSuffix(filepath.ToSlash(clean), "/") + "/"
	blobs, err := s.spills.list(prefix)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(blobs))
	for _, blob := range blobs {
		seen[blob.Key] = struct{}{}
	}
	objectPrefix := s.objectKey(prefix)
	for object := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: objectPrefix, Recursive: true}) {
		if object.Err != nil {
			return nil, fmt.Errorf("list replay S3 objects: %w", object.Err)
		}
		key := object.Key
		if s.prefix != "" {
			key = strings.TrimPrefix(key, s.prefix+"/")
		}
		if _, ok := seen[key]; ok {
			continue
		}
		blobs = append(blobs, BlobInfo{Key: key, Size: object.Size, Modified: object.LastModified})
	}
	return blobs, nil
}

func (s *S3Store) CloseSession(ctx context.Context, key string) error {
	if _, err := validateBlobPath(key); err != nil {
		return err
	}
	spill, ok := s.spills.get(key)
	if !ok {
		return nil
	}
	spill.mu.Lock()
	defer spill.mu.Unlock()
	return s.closeSpill(ctx, spill)
}

// CloseIdleSessions seals and uploads spill files whose last write predates
// idleBefore. The spill lock makes the age check atomic with sealing.
func (s *S3Store) CloseIdleSessions(ctx context.Context, idleBefore time.Time) error {
	var result error
	for _, key := range s.spills.keys() {
		spill, ok := s.spills.get(key)
		if !ok {
			continue
		}
		spill.mu.Lock()
		info, err := os.Stat(spill.path)
		if errors.Is(err, os.ErrNotExist) || err == nil && info.ModTime().After(idleBefore) {
			spill.mu.Unlock()
			continue
		}
		if err != nil {
			result = errors.Join(result, fmt.Errorf("stat replay spill: %w", err))
			spill.mu.Unlock()
			continue
		}
		result = errors.Join(result, s.closeSpill(ctx, spill))
		spill.mu.Unlock()
	}
	return result
}

func (s *S3Store) closeSpill(ctx context.Context, spill *spillFile) error {
	spill.sealed = true
	file, err := ioFileWithSize(spill.path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = s.client.PutObject(ctx, s.bucket, s.objectKey(spill.key), file, file.size, minio.PutObjectOptions{
		DisableMultipart: true, DisableContentSha256: true,
	})
	if err != nil {
		return fmt.Errorf("upload replay spill: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close replay spill: %w", err)
	}
	if err := s.spills.remove(spill); err != nil {
		return err
	}
	return nil
}

func (s *S3Store) Close(ctx context.Context) error {
	s.spills.markClosed()
	var result error
	for _, key := range s.spills.keys() {
		result = errors.Join(result, s.CloseSession(ctx, key))
	}
	return result
}

func (s *S3Store) objectKey(key string) string {
	if s.prefix == "" {
		return key
	}
	return s.prefix + "/" + key
}

func isS3Missing(err error) bool {
	if err == nil {
		return false
	}
	response := minio.ToErrorResponse(err)
	return response.StatusCode == 404 || response.Code == "NoSuchKey" || response.Code == "NotFound"
}

type sizedFile struct {
	*os.File
	size int64
}

func ioFileWithSize(name string) (*sizedFile, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open replay spill: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("stat replay spill: %w", err)
	}
	return &sizedFile{File: file, size: info.Size()}, nil
}
