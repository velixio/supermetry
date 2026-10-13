// Package updateclient is the unprivileged application's narrow client for
// the root-owned updater helper Unix socket.
package updateclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"supermetry/internal/updatehelper"
)

const operationTimeout = 5 * time.Second

var (
	ErrUnavailable = errors.New("updater helper is unavailable")
	ErrRejected    = errors.New("updater helper rejected the request")
)

type DialContext func(context.Context, string, string) (net.Conn, error)

type Client struct{ dial DialContext }

func New(dial DialContext) *Client {
	if dial == nil {
		dialer := &net.Dialer{Timeout: operationTimeout}
		dial = dialer.DialContext
	}
	return &Client{dial: dial}
}

func (c *Client) Start(ctx context.Context, request updatehelper.Request) (updatehelper.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	connection, err := c.dial(ctx, "unix", updatehelper.ControlSocketPath)
	if err != nil {
		return updatehelper.Response{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer connection.Close()
	deadline, ok := ctx.Deadline()
	if ok {
		if err := connection.SetDeadline(deadline); err != nil {
			return updatehelper.Response{}, fmt.Errorf("set updater helper deadline: %w", err)
		}
	}
	if err := updatehelper.WriteRequest(connection, request); err != nil {
		return updatehelper.Response{}, fmt.Errorf("send updater request: %w", err)
	}
	response, err := updatehelper.ReadResponse(connection)
	if err != nil {
		return updatehelper.Response{}, fmt.Errorf("read updater response: %w", err)
	}
	if response.JobID != request.JobID {
		return updatehelper.Response{}, errors.New("updater helper returned a different job ID")
	}
	if response.ErrorCode != "" {
		return response, fmt.Errorf("%w: %s", ErrRejected, response.ErrorCode)
	}
	return response, nil
}
