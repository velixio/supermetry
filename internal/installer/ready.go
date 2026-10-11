package installer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// WaitReady polls the server readiness endpoint until it succeeds or times out.
func WaitReady(ctx context.Context, baseURL string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	endpoint := strings.TrimRight(baseURL, "/") + "/readyz"
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("Supermetry was not ready within %s", timeout)
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
