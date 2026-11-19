package admin

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// MaxListLimit is the largest page the admin list endpoints serve.
const MaxListLimit = 200

const defaultListLimit = 50

// ListParams selects one page of an admin list. The zero value is the first
// page, ordered by id ascending, with the default page size.
type ListParams struct {
	Search string
	Limit  int
	// Cursor is the opaque NextCursor of the previous page. It is bound to the
	// Sort and Order it was issued for.
	Cursor string
	Sort   string
	Order  string // "asc" (default) or "desc"
	// OrgID restricts sites to one organization; zero means all.
	OrgID int64
}

// Page is one page of results; NextCursor is empty on the last page.
type Page[T any] struct {
	Items      []T
	NextCursor string
}

// sortSpec describes an allow-listed sort key: a non-null SQL expression over
// the inner query's columns and the Postgres type used to compare a cursor.
type sortSpec struct {
	expr string
	typ  string
}

type cursorToken struct {
	Sort  string `json:"s"`
	Order string `json:"o"`
	Value string `json:"v"`
	ID    int64  `json:"i"`
}

func encodeCursor(c cursorToken) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(raw string) (cursorToken, error) {
	var c cursorToken
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || json.Unmarshal(decoded, &c) != nil || c.Sort == "" || (c.Order != "asc" && c.Order != "desc") {
		return cursorToken{}, ErrInvalid
	}
	return c, nil
}

// keyset is a validated page request: the sort, direction, size and decoded
// cursor position.
type keyset struct {
	sortName  string
	spec      sortSpec
	desc      bool
	limit     int
	hasCursor bool
	cursor    cursorToken
}

func newKeyset(p ListParams, specs map[string]sortSpec) (keyset, error) {
	k := keyset{sortName: p.Sort, limit: normalizeLimit(p.Limit)}
	if k.sortName == "" {
		k.sortName = "id"
	}
	spec, ok := specs[k.sortName]
	if !ok {
		return keyset{}, ErrInvalid
	}
	k.spec = spec
	switch p.Order {
	case "", "asc":
	case "desc":
		k.desc = true
	default:
		return keyset{}, ErrInvalid
	}
	if p.Cursor != "" {
		c, err := decodeCursor(p.Cursor)
		if err != nil {
			return keyset{}, err
		}
		if c.Sort != k.sortName || (c.Order == "desc") != k.desc {
			return keyset{}, ErrInvalid
		}
		// Validate the value now so a forged cursor is a 400, not a query error.
		switch spec.typ {
		case "bigint":
			if _, err := strconv.ParseInt(c.Value, 10, 64); err != nil {
				return keyset{}, ErrInvalid
			}
		}
		k.hasCursor, k.cursor = true, c
	}
	return k, nil
}

func (k keyset) order() string {
	if k.desc {
		return "desc"
	}
	return "asc"
}

// wrap turns an inner query (which must expose an `id` column and the sort
// expression as `sk`) into the paged query. args are the inner query's
// parameters; the cursor and limit parameters are appended. The outer select is
// q.* plus the sort key as text, which pagedNext reads back.
func (k keyset) wrap(inner string, args []any) (string, []any) {
	op := ">"
	if k.desc {
		op = "<"
	}
	var cursorSQL string
	if k.hasCursor {
		args = append(args, k.cursor.Value, k.cursor.ID)
		cursorSQL = fmt.Sprintf(" where (q.sk, q.id) %s ($%d::%s, $%d::bigint)", op, len(args)-1, k.spec.typ, len(args))
	}
	args = append(args, k.limit+1)
	return fmt.Sprintf("select q.*, q.sk::text from (%s) q%s order by q.sk %s, q.id %s limit $%d",
		inner, cursorSQL, k.order(), k.order(), len(args)), args
}

// next builds the cursor for the last served row when a further row exists.
func (k keyset) next(more bool, lastID int64, lastKey string) string {
	if !more {
		return ""
	}
	return encodeCursor(cursorToken{Sort: k.sortName, Order: k.order(), Value: lastKey, ID: lastID})
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return defaultListLimit
	}
	if limit > MaxListLimit {
		return MaxListLimit
	}
	return limit
}

func cleanSearch(search string) (string, error) {
	search = strings.TrimSpace(search)
	if len(search) > 200 {
		return "", ErrInvalid
	}
	return search, nil
}
