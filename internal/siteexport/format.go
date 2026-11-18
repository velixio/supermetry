package siteexport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// SchemaVersion is the version of the export format described by manifest.json
// and the JSON Schemas in docs/export-schemas. Bump it when a column is
// removed or changes meaning; adding a table or a trailing column is also a
// bump so consumers can tell exports apart.
const SchemaVersion = 1

// ManifestName is the archive entry that lists every other entry.
const ManifestName = "manifest.json"

// Column types used in the table registry. Every cell is a CSV string; the type
// says how to read it. A column marked optional may be an empty string.
const (
	typeString = "string"
	typeInt    = "integer"
	typeDate   = "date"
	typeTime   = "timestamp"
	typeJSON   = "json"
	typeBool   = "boolean"
	typeNumber = "number"
)

// Column documents one CSV column.
type Column struct {
	Name     string
	Type     string
	Optional bool
}

// Table documents one exported CSV file.
type Table struct {
	File        string
	Description string
	Columns     []Column
}

// cols parses "name:type name:type?" into columns; a trailing ? marks an
// optional (possibly empty) cell.
func cols(spec string) []Column {
	fields := strings.Fields(spec)
	out := make([]Column, 0, len(fields))
	for _, field := range fields {
		name, typ, ok := strings.Cut(field, ":")
		if !ok {
			panic("siteexport: bad column spec " + field)
		}
		optional := strings.HasSuffix(typ, "?")
		out = append(out, Column{Name: name, Type: strings.TrimSuffix(typ, "?"), Optional: optional})
	}
	return out
}

const dimensionColumns = "day:date value:string visitors:integer visits:integer pageviews:integer bounces:integer visit_duration_s:integer events:integer"

// Tables is the registry of every CSV the exporter can write, in the order
// they appear in the archive. A test fails when an export contains a file or
// header that is not described here, so a new column cannot ship without a
// schema update.
func Tables() []Table {
	t := []Table{{
		File: "daily_totals.csv", Description: "Site totals per day, native rollups plus imported history before the first native day.",
		Columns: cols("day:date visitors:integer visits:integer pageviews:integer bounces:integer visit_duration_s:integer"),
	}}
	for _, d := range dimensions[:4] {
		t = append(t, Table{File: d.file, Description: "Per-day breakdown by " + strings.TrimSuffix(d.file, ".csv") + ".", Columns: cols(dimensionColumns)})
	}
	for _, a := range attributions {
		t = append(t, Table{File: a.file, Description: "Per-day sessions attributed to each " + strings.TrimSuffix(strings.TrimPrefix(a.file, "utm_"), "s.csv") + " value.", Columns: cols(dimensionColumns)})
	}
	for _, d := range dimensions[4:] {
		t = append(t, Table{File: d.file, Description: "Per-day breakdown by " + strings.TrimSuffix(d.file, ".csv") + ".", Columns: cols(dimensionColumns)})
	}
	t = append(t, Table{
		File: "custom_events.csv", Description: "Custom events per day, with properties and revenue.",
		Columns: cols("day:date event:string page:string props:json events:integer revenue_micros:integer? revenue_currency:string?"),
	})
	return append(t, appTables()...)
}

func appTables() []Table {
	return []Table{
		{"app_issues.csv", "Grouped error issues that were active in the range.", cols("issue_id:integer title:string culprit:string level:string status:string kind:string error_type:string first_seen:timestamp last_seen:timestamp occurrences:integer first_release:string last_release:string resolved_in_release:string? priority:string assignee_id:integer? github_url:string? merged_into:integer?")},
		{"app_issue_activity.csv", "Issue timeline entries (status changes, assignments).", cols("activity_id:integer issue_id:integer at:timestamp kind:string actor_id:integer? data:json")},
		{"app_issue_comments.csv", "Comments on issues.", cols("comment_id:integer issue_id:integer author_id:integer? created:timestamp edited:timestamp? text:string")},
		{"app_ownership_rules.csv", "Ownership rules in evaluation order.", cols("position:integer matcher:string value:string owner_id:integer")},
		{"app_grouping_rules.csv", "Fingerprint grouping rules in evaluation order.", cols("position:integer matcher:string value:string fingerprint:string")},
		{"app_saved_views.csv", "Shared saved issue searches.", cols("view_id:integer name:string query:string owner_id:integer? created:timestamp")},
		{"app_events.csv", "Stored error events (no bodies, no user identifiers).", cols("event_id:string timestamp:timestamp issue_id:integer? kind:string level:string release:string dist:string environment:string platform:string sdk_name:string sdk_version:string os_version:string arch:string device_class:string runtime:string symbolication:string occurrences:integer")},
		{"app_sessions_daily.csv", "Release-health session counts per day.", cols("day:date release:string environment:string platform:string sessions:integer errored:integer crashed:integer abnormal:integer users:integer crashed_users:integer")},
		{"app_releases.csv", "Releases created in the range.", cols("release_id:integer version:string url:string created:timestamp released:timestamp?")},
		{"app_feedback.csv", "User feedback; the reporter's email is not exported.", cols("feedback_id:integer issue_id:integer? event_id:string? at:timestamp has_contact:boolean text:string page:string?")},
		{"app_monitors.csv", "Cron monitor configuration.", cols("monitor_id:integer slug:string environment:string schedule_type:string schedule:string timezone:string checkin_margin_minutes:integer max_runtime_minutes:integer failure_threshold:integer recovery_threshold:integer status:string last_checkin:timestamp? next_expected:timestamp?")},
		{"app_checkins.csv", "Cron monitor check-ins in the range.", cols("monitor_id:integer checkin_id:string timestamp:timestamp status:string expected_at:timestamp? duration_ms:integer? release:string?")},
		{"app_deploys.csv", "Deploys created in the range.", cols("deploy_id:integer release:string environment:string name:string url:string started:timestamp? finished:timestamp? created:timestamp")},
		{"app_commits.csv", "Commits attached to releases created in the range.", cols("release:string repository:string sha:string message:string author_name:string author_email:string committed_at:timestamp")},
		{"app_client_reports.csv", "Daily counts of events SDKs report they discarded.", cols("day:date reason:string category:string quantity:integer")},
		{"metric_series.csv", "Metric series registry.", cols("series_id:integer name:string kind:string unit:string attributes:json first_seen:timestamp last_seen:timestamp bounds:string?")},
		{"metric_hourly.csv", "Hourly metric rollups in the site's local hours.", cols("series_id:integer local_hour:timestamp samples:integer value_sum:number? value_min:number? value_max:number? value_last:number? count:number? sum:number? bucket_counts:string?")},
		{"app_active_users.csv", "Daily, weekly and monthly active installs and new installs.", cols("day:date dau:integer wau:integer mau:integer new_installs:integer")},
		{"app_transactions_hourly.csv", "Hourly transaction rollups with p50 and p95.", cols("local_hour:timestamp name:string op:string release:string environment:string platform:string count:integer errors:integer p50_ms:number? p95_ms:number?")},
		{"app_span_groups.csv", "Child span groups: operation and parameterised label.", cols("group_id:integer op:string label:string first_seen:timestamp")},
		{"app_event_attachments.csv", "Attachment listing; bytes are under app_attachments/.", cols("event_id:string index:integer name:string content_type:string size_bytes:integer created:timestamp file:string")},
		{"app_artifacts.csv", "Source map and debug artifact bundles (metadata only).", cols("bundle_id:integer release:string dist:string state:string debug_ids:string? file_count:integer size_bytes:integer created:timestamp")},
		{"app_debug_files.csv", "Uploaded debug files (metadata only).", cols("file_id:integer kind:string debug_id:string code_id:string arch:string object_name:string features:string? cache_state:string size_bytes:integer created:timestamp")},
		{"logs.csv", "Log records inside the range and the site's log retention.", cols("timestamp:timestamp severity:integer service:string environment:string release:string trace_id:string? body:string attributes:json")},
	}
}

// TableFor returns the registry entry for an archive file name.
func TableFor(file string) (Table, bool) {
	for _, t := range Tables() {
		if t.File == file {
			return t, true
		}
	}
	return Table{}, false
}

// schemaBase is the id prefix of the published schemas.
const schemaBase = "https://supermetry.com/schemas/export/v1/"

// SchemaFile is the published JSON Schema file name of a table.
func SchemaFile(t Table) string { return strings.TrimSuffix(t.File, ".csv") + ".schema.json" }

// JSONSchema renders the JSON Schema (draft 2020-12) of one row of a table.
// Rows are described as the values of the CSV cells after parsing: integers as
// JSON integers, optional empty cells as null.
func JSONSchema(t Table) []byte {
	props := map[string]any{}
	required := make([]string, 0, len(t.Columns))
	for _, c := range t.Columns {
		var p map[string]any
		switch c.Type {
		case typeInt:
			p = map[string]any{"type": "integer"}
		case typeNumber:
			p = map[string]any{"type": "number"}
		case typeBool:
			p = map[string]any{"type": "boolean"}
		case typeDate:
			p = map[string]any{"type": "string", "format": "date"}
		case typeTime:
			p = map[string]any{"type": "string", "format": "date-time"}
		case typeJSON:
			p = map[string]any{"type": "string", "description": "A JSON document stored as text."}
		default:
			p = map[string]any{"type": "string"}
		}
		if c.Optional {
			p["type"] = []string{p["type"].(string), "null"}
			p["description"] = "An empty CSV cell is null."
		}
		props[c.Name] = p
		required = append(required, c.Name)
	}
	doc := map[string]any{
		"$schema":                     "https://json-schema.org/draft/2020-12/schema",
		"$id":                         schemaBase + SchemaFile(t),
		"title":                       t.File,
		"description":                 t.Description,
		"type":                        "object",
		"properties":                  props,
		"required":                    required,
		"additionalProperties":        false,
		"x-supermetry-schema-version": SchemaVersion,
		"x-supermetry-columns":        columnNames(t),
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(doc)
	return buf.Bytes()
}

func columnNames(t Table) []string {
	out := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		out[i] = c.Name
	}
	return out
}

// ManifestSchema is the JSON Schema of manifest.json.
func ManifestSchema() []byte { return manifestSchema }

var manifestSchema = []byte(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "` + schemaBase + `manifest.schema.json",
  "title": "manifest.json",
  "description": "Lists every file of a Supermetry site export with a checksum.",
  "type": "object",
  "required": ["schema_version", "generated_at", "generator", "range", "site", "files"],
  "additionalProperties": false,
  "properties": {
    "schema_version": {"type": "integer", "const": ` + fmt.Sprint(SchemaVersion) + `},
    "generated_at": {"type": "string", "format": "date-time"},
    "generator": {"type": "string"},
    "range": {"type": "object", "required": ["from", "to"], "additionalProperties": false,
      "properties": {"from": {"type": "string", "format": "date"}, "to": {"type": "string", "format": "date"}}},
    "site": {"type": "object", "required": ["id", "public_id", "name", "kind", "timezone"], "additionalProperties": false,
      "properties": {"id": {"type": "integer"}, "public_id": {"type": "string"}, "name": {"type": "string"},
        "kind": {"type": "string", "enum": ["web", "pixel", "app"]}, "timezone": {"type": "string"}}},
    "files": {"type": "array", "items": {"type": "object",
      "required": ["path", "format", "bytes", "sha256"], "additionalProperties": false,
      "properties": {
        "path": {"type": "string"},
        "format": {"type": "string", "enum": ["csv", "binary"]},
        "rows": {"type": "integer", "minimum": 0, "description": "Data rows, excluding the CSV header (csv only)."},
        "bytes": {"type": "integer", "minimum": 0, "description": "Uncompressed size."},
        "sha256": {"type": "string", "pattern": "^[0-9a-f]{64}$", "description": "Of the uncompressed content."},
        "columns": {"type": "array", "items": {"type": "string"}},
        "schema": {"type": "string", "description": "File name of the row schema under the published schema directory."}
      }}}
  }
}
`)

// AllSchemas returns every published schema file name and content: one per
// table plus the manifest schema, sorted by name.
func AllSchemas() map[string][]byte {
	out := map[string][]byte{"manifest.schema.json": manifestSchema}
	for _, t := range Tables() {
		out[SchemaFile(t)] = JSONSchema(t)
	}
	return out
}

// SchemaNames lists AllSchemas keys in order.
func SchemaNames() []string {
	m := AllSchemas()
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
