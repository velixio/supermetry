package replay

import (
	"context"
	"io"
	"strconv"
	"time"
)

type BlobStore interface {
	Append(ctx context.Context, key string, r io.Reader) (int64, error)
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	PutIfAbsent(ctx context.Context, key string, r io.Reader) (stored bool, err error)
	DeletePrefix(ctx context.Context, prefix string) (int64, error)
	Delete(ctx context.Context, key string) (int64, error)
	List(ctx context.Context, prefix string) ([]BlobInfo, error)
}

type BlobInfo struct {
	Key      string
	Size     int64
	Modified time.Time
}

func SessionKey(siteID int32, day time.Time, sessionID int64) string {
	return "replay/" + strconv.FormatInt(int64(siteID), 10) + "/" + day.Format(time.DateOnly) + "/" + strconv.FormatInt(sessionID, 10) + ".gz"
}

func CSSKey(siteID int32, sha256hex string) string {
	return "replay/" + strconv.FormatInt(int64(siteID), 10) + "/css/" + sha256hex + ".gz"
}
