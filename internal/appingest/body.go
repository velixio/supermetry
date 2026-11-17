package appingest

import (
	"time"

	"supermetry/internal/appattach"
)

// Body is the scrubbed detail of one Sentry-style event. It is serialised as
// JSON into app_event_bodies.body. The key fields (SiteID, TS, EventID) are
// table columns and stay out of the JSON; Row carries the extra app_events
// columns and stays out too.
type Body struct {
	SiteID        int32                     `json:"-"`
	TS            time.Time                 `json:"-"`
	EventID       string                    `json:"-"`
	Row           RowExtras                 `json:"-"`
	Release       string                    `json:"release,omitempty"`
	Symbolication string                    `json:"symbolication,omitempty"` // "", "ok", "missing" or "partial"
	DebugImages   []DebugImage              `json:"-"`                       // in memory only, for symbolication
	Level         string                    `json:"level,omitempty"`
	Platform      string                    `json:"platform,omitempty"`
	Message       string                    `json:"message,omitempty"`
	Country       string                    `json:"country,omitempty"`
	Exceptions    []Exception               `json:"exceptions,omitempty"`
	Breadcrumbs   []Breadcrumb              `json:"breadcrumbs,omitempty"`
	Contexts      map[string]map[string]any `json:"contexts,omitempty"`
	Request       *Request                  `json:"request,omitempty"`
	Tags          map[string]string         `json:"tags,omitempty"`
	Attachments   []AttachmentMeta          `json:"attachments,omitempty"`
	Files         []appattach.File          `json:"-"` // scrubbed content, stored beside the body
	SDK           SDKInfo                   `json:"sdk"`
}

// RowExtras are values the normaliser derives for columns of app_events and
// app_issues beyond the v1 Event struct.
type RowExtras struct {
	Title           string // issue title: scrubbed "Type: value" or message
	MessageHash     []byte // sha256 of the scrubbed title text; nil when none
	UserHash        []byte // HMAC of user.id with the app secret; nil when unknown
	Culprit         string
	Transaction     string
	Dist            string
	SDKName         string
	SDKVersion      string
	GroupingVersion int16
	SDKFingerprint  []string // the SDK's custom fingerprint, kept to regroup after symbolication
}

// DebugImage is one debug_meta image of a Sentry event; only the fields
// symbolication needs. It never reaches storage.
type DebugImage struct {
	Type     string
	CodeFile string
	DebugID  string
}

// Exception is one entry of an exception chain, oldest first.
type Exception struct {
	Type      string     `json:"type,omitempty"`
	Value     string     `json:"value,omitempty"`
	Module    string     `json:"module,omitempty"`
	Mechanism *Mechanism `json:"mechanism,omitempty"`
	Frames    []Frame    `json:"frames,omitempty"` // oldest call first, as the SDKs send them
}

// Mechanism says how the SDK caught the exception.
type Mechanism struct {
	Type    string `json:"type,omitempty"`
	Handled *bool  `json:"handled,omitempty"`
	Signal  string `json:"signal,omitempty"`
}

// Frame is one scrubbed stack frame. Local variables are never kept.
type Frame struct {
	Filename        string   `json:"filename,omitempty"`
	AbsPath         string   `json:"abs_path,omitempty"` // the minified file's path, kept on frames symbolication resolved
	AbsURL          string   `json:"-"`                  // the reported abs_path or filename (query stripped), in memory only
	Module          string   `json:"module,omitempty"`
	Function        string   `json:"function,omitempty"`
	Package         string   `json:"package,omitempty"`
	Lineno          int      `json:"lineno,omitempty"`
	Colno           int      `json:"colno,omitempty"`
	InApp           bool     `json:"in_app,omitempty"`
	InstructionAddr string   `json:"instruction_addr,omitempty"`
	PreContext      []string `json:"pre_context,omitempty"`
	ContextLine     string   `json:"context_line,omitempty"`
	PostContext     []string `json:"post_context,omitempty"`
}

// Breadcrumb is one scrubbed trail entry.
type Breadcrumb struct {
	Type      string         `json:"type,omitempty"`
	Category  string         `json:"category,omitempty"`
	Level     string         `json:"level,omitempty"`
	Timestamp float64        `json:"timestamp,omitempty"` // unix seconds
	Message   string         `json:"message,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}

// Request keeps only what is safe: method, path (no query string, no host),
// and a small header allowlist. Cookies, credentials and bodies are dropped.
type Request struct {
	Method  string            `json:"method,omitempty"`
	Path    string            `json:"path,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// AttachmentMeta describes an attachment; Stored says whether its content was
// kept (see package appattach).
type AttachmentMeta = appattach.Meta

// SDKInfo names the SDK that sent the event.
type SDKInfo struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}
