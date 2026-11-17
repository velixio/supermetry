// Package sentryingest is the front door for Sentry SDK traffic: it parses
// envelopes, authenticates by DSN public key against the in-memory site
// registry and hands events to appingest. It never reads the database on the
// request path.
package sentryingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"time"
)

// Parse limits of the envelope format.
const (
	MaxItems         = 500
	MaxEnvelopeBytes = 20 << 20
	MaxEventBytes    = 1 << 20
	// MaxUnauthenticatedBytes caps the compressed body of a request that
	// carries no key in a header or the query.
	MaxUnauthenticatedBytes = 2 << 20
	MaxAttachment           = 1 << 20
)

// Errors a caller maps to HTTP statuses.
var (
	ErrMalformed   = errors.New("malformed envelope")
	ErrTooManyItem = errors.New("too many envelope items")
	ErrTooLarge    = errors.New("envelope too large")
)

// SDKInfo is the sdk object of an envelope header.
type SDKInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// EnvelopeHeader is the first line of an envelope.
type EnvelopeHeader struct {
	EventID string
	DSN     string
	SentAt  time.Time // zero when absent or unparseable
	SDK     SDKInfo
}

// RequestMeta is what the handler knows about the HTTP request. The client
// address is only for the geo lookup that already ran; nothing here is stored.
type RequestMeta struct {
	ClientIP  netip.Addr // zero when unknown
	Country   string     // ISO code from the geo lookup, empty when unknown
	UserAgent string     // raw; the normaliser parses it and discards it
	Origin    string
	SDKClient string // sentry_client query value or auth field
}

// Item is one envelope item. Payload aliases the decoded body and must not be
// retained past the request unless copied.
type Item struct {
	Type           string
	ContentType    string
	AttachmentType string
	Filename       string
	Payload        []byte
}

// Envelope is a parsed envelope.
type Envelope struct {
	Header EnvelopeHeader
	Items  []Item
}

// Limits bounds ParseEnvelope. Zero values use the package defaults.
type Limits struct {
	MaxBytes int64
	MaxItems int
}

type rawHeader struct {
	EventID string  `json:"event_id"`
	DSN     string  `json:"dsn"`
	SentAt  string  `json:"sent_at"`
	SDK     SDKInfo `json:"sdk"`
}

type rawItemHeader struct {
	Type           string `json:"type"`
	Length         *int64 `json:"length"`
	ContentType    string `json:"content_type"`
	AttachmentType string `json:"attachment_type"`
	Filename       string `json:"filename"`
}

// ParseEnvelope parses the envelope wire format: a JSON header line, then
// items of a JSON item header line and a payload. A payload with a length is
// exactly that many bytes; without one it ends at the next newline or the end
// of the input. The newline after a payload and the final newline are
// optional, and empty lines between items are skipped. Unknown item types are
// returned as they are so the caller can count them.
func ParseEnvelope(r io.Reader, limits Limits) (Envelope, error) {
	if limits.MaxBytes <= 0 {
		limits.MaxBytes = MaxEnvelopeBytes
	}
	if limits.MaxItems <= 0 {
		limits.MaxItems = MaxItems
	}
	data, err := readLimited(r, limits.MaxBytes)
	if err != nil {
		return Envelope{}, err
	}
	return parseEnvelopeBytes(data, limits)
}

func parseEnvelopeBytes(data []byte, limits Limits) (Envelope, error) {
	if limits.MaxItems <= 0 {
		limits.MaxItems = MaxItems
	}
	line, rest := cutLine(data)
	var header rawHeader
	if err := json.Unmarshal(bytes.TrimSpace(line), &header); err != nil {
		return Envelope{}, fmt.Errorf("%w: header: %v", ErrMalformed, err)
	}
	env := Envelope{Header: EnvelopeHeader{EventID: header.EventID, DSN: header.DSN, SDK: header.SDK}}
	if header.SentAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, header.SentAt); err == nil {
			env.Header.SentAt = t.UTC()
		}
	}
	for len(rest) > 0 {
		line, rest = cutLine(rest)
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var ih rawItemHeader
		if err := json.Unmarshal(bytes.TrimSpace(line), &ih); err != nil {
			return Envelope{}, fmt.Errorf("%w: item header: %v", ErrMalformed, err)
		}
		if len(env.Items) >= limits.MaxItems {
			return Envelope{}, ErrTooManyItem
		}
		item := Item{Type: ih.Type, ContentType: ih.ContentType, AttachmentType: ih.AttachmentType, Filename: ih.Filename}
		if ih.Length != nil {
			n := *ih.Length
			if n < 0 || n > int64(len(rest)) {
				return Envelope{}, fmt.Errorf("%w: item length %d exceeds the body", ErrMalformed, n)
			}
			item.Payload = rest[:n]
			rest = rest[n:]
			if len(rest) > 0 && rest[0] == '\n' {
				rest = rest[1:]
			} else if len(rest) > 1 && rest[0] == '\r' && rest[1] == '\n' {
				rest = rest[2:]
			}
		} else {
			item.Payload, rest = cutLine(rest)
		}
		env.Items = append(env.Items, item)
	}
	return env, nil
}

// cutLine splits at the first newline; the line has no trailing \r or \n.
func cutLine(data []byte) (line, rest []byte) {
	i := bytes.IndexByte(data, '\n')
	if i < 0 {
		return bytes.TrimSuffix(data, []byte{'\r'}), nil
	}
	return bytes.TrimSuffix(data[:i], []byte{'\r'}), data[i+1:]
}

// readLimited reads up to max bytes in chunks, so a stream that exceeds the
// cap is rejected after about max bytes of memory, never a doubling copy.
func readLimited(r io.Reader, max int64) ([]byte, error) {
	const chunkSize = 256 << 10
	var chunks [][]byte
	var total int64
	for {
		chunk := make([]byte, chunkSize)
		n, err := io.ReadFull(r, chunk)
		total += int64(n)
		if total > max {
			return nil, ErrTooLarge
		}
		if n > 0 {
			chunks = append(chunks, chunk[:n])
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if len(chunks) == 1 {
		return chunks[0], nil
	}
	out := make([]byte, 0, total)
	for _, c := range chunks {
		out = append(out, c...)
	}
	return out, nil
}
