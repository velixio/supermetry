package updaterdaemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"supermetry/internal/updatehelper"
)

const defaultConnectionTimeout = 5 * time.Second

type Server struct {
	Listener          net.Listener
	Controller        *Controller
	ConnectionTimeout time.Duration
}

func (s *Server) Serve(ctx context.Context) error {
	if s.Listener == nil || s.Controller == nil {
		return errors.New("updater server is not configured")
	}
	if s.ConnectionTimeout <= 0 {
		s.ConnectionTimeout = defaultConnectionTimeout
	}
	if err := s.Controller.Recover(ctx); err != nil {
		return fmt.Errorf("recover updater state: %w", err)
	}
	stop := context.AfterFunc(ctx, func() { _ = s.Listener.Close() })
	defer stop()
	defer s.Controller.Wait()
	for {
		connection, err := s.Listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept updater connection: %w", err)
		}
		s.handleConnection(ctx, connection)
	}
}

func (s *Server) handleConnection(ctx context.Context, connection net.Conn) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(s.ConnectionTimeout))
	request, err := updatehelper.ReadRequest(connection)
	if err != nil {
		return
	}
	response := s.Controller.Handle(ctx, request)
	_ = updatehelper.WriteResponse(connection, response)
}

// SystemdListener obtains the Unix listener passed by systemd socket
// activation. The service intentionally has no fallback that creates a more
// permissive socket itself.
func SystemdListener() (net.Listener, error) {
	const listenFDsStart = 3
	if os.Getenv("LISTEN_PID") != fmt.Sprint(os.Getpid()) || os.Getenv("LISTEN_FDS") != "1" {
		return nil, errors.New("exactly one systemd socket activation file descriptor is required")
	}
	file := os.NewFile(listenFDsStart, "supermetry-updater.socket")
	if file == nil {
		return nil, errors.New("systemd socket descriptor is unavailable")
	}
	listener, err := net.FileListener(file)
	closeErr := file.Close()
	if err != nil {
		return nil, fmt.Errorf("open systemd socket: %w", err)
	}
	if closeErr != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("close inherited socket descriptor: %w", closeErr)
	}
	if listener.Addr().Network() != "unix" {
		_ = listener.Close()
		return nil, errors.New("systemd updater socket must be a Unix socket")
	}
	return listener, nil
}
