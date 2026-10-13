// Package updatehelper contains the narrow, durable control plane shared by the
// unprivileged application and the privileged updater helper.
package updatehelper

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
)

const (
	ProtocolVersion   = 1
	MaxFrameSize      = 4096
	ControlSocketPath = "/run/supermetry-updater/control.sock"
)

var (
	versionPattern   = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	errorCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

type Request struct {
	Version       int    `json:"version"`
	JobID         string `json:"job_id"`
	TargetVersion string `json:"target_version"`
	Nonce         string `json:"nonce"`
}

type Response struct {
	Version   int    `json:"version"`
	JobID     string `json:"job_id"`
	State     State  `json:"state"`
	ErrorCode string `json:"error_code,omitempty"`
}

func (r Request) Validate() error {
	if r.Version != ProtocolVersion {
		return fmt.Errorf("unsupported protocol version %d", r.Version)
	}
	if err := validateJobID(r.JobID); err != nil {
		return err
	}
	if err := validateStableVersion(r.TargetVersion); err != nil {
		return err
	}
	nonce, err := base64.RawURLEncoding.DecodeString(r.Nonce)
	if err != nil || len(nonce) != 32 || base64.RawURLEncoding.EncodeToString(nonce) != r.Nonce {
		return errors.New("nonce must be a 32-byte unpadded base64url value")
	}
	return nil
}

func ReadRequest(r io.Reader) (Request, error) {
	var request Request
	if err := readFrame(r, &request, "version", "job_id", "target_version", "nonce"); err != nil {
		return Request{}, err
	}
	if err := request.Validate(); err != nil {
		return Request{}, fmt.Errorf("validate request: %w", err)
	}
	return request, nil
}

func WriteRequest(w io.Writer, request Request) error {
	if err := request.Validate(); err != nil {
		return fmt.Errorf("validate request: %w", err)
	}
	return writeFrame(w, request)
}

func ReadResponse(r io.Reader) (Response, error) {
	var response Response
	if err := readFrame(r, &response, "version", "job_id", "state", "error_code"); err != nil {
		return Response{}, err
	}
	if response.Version != ProtocolVersion {
		return Response{}, fmt.Errorf("unsupported protocol version %d", response.Version)
	}
	if err := validateJobID(response.JobID); err != nil {
		return Response{}, err
	}
	if !response.State.Valid() {
		return Response{}, fmt.Errorf("invalid state %q", response.State)
	}
	if response.ErrorCode != "" && !errorCodePattern.MatchString(response.ErrorCode) {
		return Response{}, errors.New("invalid error code")
	}
	return response, nil
}

func WriteResponse(w io.Writer, response Response) error {
	if response.Version != ProtocolVersion {
		return fmt.Errorf("unsupported protocol version %d", response.Version)
	}
	if err := validateJobID(response.JobID); err != nil {
		return err
	}
	if !response.State.Valid() {
		return fmt.Errorf("invalid state %q", response.State)
	}
	if response.ErrorCode != "" && !errorCodePattern.MatchString(response.ErrorCode) {
		return errors.New("invalid error code")
	}
	return writeFrame(w, response)
}

func readFrame(r io.Reader, dst any, allowedFields ...string) error {
	var size uint32
	if err := binary.Read(r, binary.BigEndian, &size); err != nil {
		return fmt.Errorf("read frame length: %w", err)
	}
	if size == 0 || size > MaxFrameSize {
		return fmt.Errorf("invalid frame length %d", size)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return fmt.Errorf("read frame body: %w", err)
	}
	if err := decodeStrictObject(payload, dst, allowedFields...); err != nil {
		return fmt.Errorf("decode frame: %w", err)
	}
	return nil
}

func decodeStrictObject(payload []byte, dst any, allowedFields ...string) error {
	allowed := make(map[string]struct{}, len(allowedFields))
	for _, field := range allowedFields {
		allowed[field] = struct{}{}
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	start, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := start.(json.Delim); !ok || delimiter != '{' {
		return errors.New("expected JSON object")
	}
	fields := make(map[string]json.RawMessage, len(allowedFields))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := token.(string)
		if !ok {
			return errors.New("expected object field")
		}
		if _, ok := allowed[name]; !ok {
			return fmt.Errorf("unknown field %q", name)
		}
		if _, duplicate := fields[name]; duplicate {
			return fmt.Errorf("duplicate field %q", name)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return err
		}
		fields[name] = raw
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing data")
	}
	canonical, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return json.Unmarshal(canonical, dst)
}

func writeFrame(w io.Writer, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode frame: %w", err)
	}
	if len(payload) == 0 || len(payload) > MaxFrameSize {
		return fmt.Errorf("invalid frame length %d", len(payload))
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if err := writeAll(w, header[:]); err != nil {
		return fmt.Errorf("write frame length: %w", err)
	}
	if err := writeAll(w, payload); err != nil {
		return fmt.Errorf("write frame body: %w", err)
	}
	return nil
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := w.Write(data)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(data) {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func validateJobID(id string) error {
	if id == "" || id[0] == '0' || len(id) > 19 {
		return errors.New("job ID must be a canonical positive decimal bigint")
	}
	value, err := strconv.ParseInt(id, 10, 64)
	if err != nil || value <= 0 {
		return errors.New("job ID must be a canonical positive decimal bigint")
	}
	return nil
}

func validateStableVersion(version string) error {
	if len(version) > 64 || !versionPattern.MatchString(version) {
		return errors.New("target version must be an exact stable semantic version")
	}
	return nil
}
