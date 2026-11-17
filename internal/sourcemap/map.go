package sourcemap

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Mapping is a resolved original position. Line is 1-based and Col is
// 0-based, as in the maps themselves.
type Mapping struct {
	Source  string // sourceRoot joined to the source name
	Line    int    // 1-based
	Col     int    // 0-based
	Name    string
	HasName bool
}

type segment struct {
	genLine, genCol int32
	src             int32 // -1: the segment maps nowhere
	srcLine, srcCol int32
	name            int32 // -1: no name
}

// Map is a parsed source map. Index maps are flattened at parse time.
type Map struct {
	File           string
	SourceRoot     string
	DebugID        string
	Sources        []string // sourceRoot already applied
	SourcesContent []*string
	Names          []string

	segs   []segment // sorted by (genLine, genCol)
	byName map[string]int

	scopes *Scopes // optional; set once before the map is shared
}

type rawMap struct {
	Version        int               `json:"version"`
	File           string            `json:"file"`
	SourceRoot     string            `json:"sourceRoot"`
	Sources        []*string         `json:"sources"`
	SourcesContent []*string         `json:"sourcesContent"`
	Names          []json.RawMessage `json:"names"`
	Mappings       *string           `json:"mappings"`
	DebugID        string            `json:"debugId"`
	DebugIDSnake   string            `json:"debug_id"`
	Sections       []rawSection      `json:"sections"`
}

type rawSection struct {
	Offset struct {
		Line   int `json:"line"`
		Column int `json:"column"`
	} `json:"offset"`
	URL *string         `json:"url"`
	Map json.RawMessage `json:"map"`
}

// ParseMap parses a regular or index (sections) source map.
func ParseMap(b []byte) (*Map, error) {
	var r rawMap
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("sourcemap: %w", err)
	}
	if r.Version != 3 {
		return nil, fmt.Errorf("sourcemap: unsupported version %d", r.Version)
	}
	m := &Map{File: r.File, SourceRoot: r.SourceRoot, DebugID: NormalizeDebugID(firstNonEmpty(r.DebugID, r.DebugIDSnake))}
	if r.Sections != nil {
		if r.Mappings != nil {
			return nil, errors.New("sourcemap: both sections and mappings")
		}
		prevLine, prevCol := -1, -1
		for i, s := range r.Sections {
			if s.URL != nil {
				return nil, errors.New("sourcemap: section url is not supported")
			}
			l, c := s.Offset.Line, s.Offset.Column
			if l < 0 || c < 0 || l > maxVLQ || c > maxVLQ {
				return nil, fmt.Errorf("sourcemap: section %d: bad offset", i)
			}
			if l < prevLine || (l == prevLine && c < prevCol) {
				return nil, fmt.Errorf("sourcemap: section %d: offsets not ascending", i)
			}
			prevLine, prevCol = l, c
			var sub rawMap
			if err := json.Unmarshal(s.Map, &sub); err != nil {
				return nil, fmt.Errorf("sourcemap: section %d: %w", i, err)
			}
			if sub.Version != 3 || sub.Sections != nil || sub.Mappings == nil {
				return nil, fmt.Errorf("sourcemap: section %d: not a regular v3 map", i)
			}
			if err := m.appendMap(&sub, l, c); err != nil {
				return nil, fmt.Errorf("sourcemap: section %d: %w", i, err)
			}
		}
	} else {
		if r.Mappings == nil {
			return nil, errors.New("sourcemap: missing mappings")
		}
		if err := m.appendMap(&r, 0, 0); err != nil {
			return nil, err
		}
	}
	// Stable: segments of one line keep generated order from the decoder.
	sort.SliceStable(m.segs, func(i, j int) bool {
		a, b := m.segs[i], m.segs[j]
		if a.genLine != b.genLine {
			return a.genLine < b.genLine
		}
		return a.genCol < b.genCol
	})
	m.byName = make(map[string]int, len(m.Sources))
	for i, s := range m.Sources {
		if _, ok := m.byName[s]; !ok {
			m.byName[s] = i
		}
	}
	return m, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// appendMap decodes sub into m, shifting generated positions by the section
// offset (the column shift applies to the first generated line only).
func (m *Map) appendMap(sub *rawMap, lineOff, colOff int) error {
	srcBase, nameBase := len(m.Sources), len(m.Names)
	for i, s := range sub.Sources {
		name := ""
		if s != nil {
			name = joinRoot(sub.SourceRoot, *s)
		}
		m.Sources = append(m.Sources, name)
		var content *string
		if i < len(sub.SourcesContent) {
			content = sub.SourcesContent[i]
		}
		m.SourcesContent = append(m.SourcesContent, content)
	}
	for _, n := range sub.Names {
		var s string
		if json.Unmarshal(n, &s) != nil {
			s = ""
		}
		m.Names = append(m.Names, s)
	}
	nSrc, nName := len(sub.Sources), len(sub.Names)
	mappings := *sub.Mappings
	var vals []int
	genLine, genCol, src, srcLine, srcCol, name := 0, 0, 0, 0, 0, 0
	for len(mappings) > 0 {
		// One generated line: up to the next ';'.
		line := mappings
		rest := ""
		if i := strings.IndexByte(mappings, ';'); i >= 0 {
			line, rest = mappings[:i], mappings[i+1:]
		}
		mappings = rest
		genCol = 0
		for len(line) > 0 {
			seg := line
			tail := ""
			if i := strings.IndexByte(line, ','); i >= 0 {
				seg, tail = line[:i], line[i+1:]
			}
			line = tail
			if seg == "" {
				continue
			}
			var err error
			vals, err = decodeSegment(seg, vals)
			if err != nil {
				return err
			}
			if n := len(vals); n != 1 && n != 4 && n != 5 {
				return fmt.Errorf("sourcemap: segment with %d fields", n)
			}
			genCol += vals[0]
			if genCol < 0 || genCol > maxVLQ {
				return errors.New("sourcemap: generated column out of range")
			}
			s := segment{genLine: int32(genLine + lineOff), genCol: int32(genCol), src: -1, name: -1}
			if genLine == 0 {
				c := genCol + colOff
				if c > maxVLQ {
					return errors.New("sourcemap: generated column out of range")
				}
				s.genCol = int32(c)
			}
			if int64(genLine)+int64(lineOff) > maxVLQ {
				return errors.New("sourcemap: generated line out of range")
			}
			if len(vals) >= 4 {
				src += vals[1]
				srcLine += vals[2]
				srcCol += vals[3]
				if src < 0 || src >= nSrc || srcLine < 0 || srcCol < 0 || srcLine > maxVLQ || srcCol > maxVLQ {
					return errors.New("sourcemap: source position out of range")
				}
				s.src, s.srcLine, s.srcCol = int32(srcBase+src), int32(srcLine), int32(srcCol)
				if len(vals) == 5 {
					name += vals[4]
					if name < 0 || name >= nName {
						return errors.New("sourcemap: name index out of range")
					}
					s.name = int32(nameBase + name)
				}
			}
			m.segs = append(m.segs, s)
		}
		genLine++
	}
	return nil
}

func joinRoot(root, src string) string {
	if root == "" || src == "" {
		return src
	}
	if strings.HasPrefix(src, "/") || strings.Contains(src, "://") {
		return src
	}
	return strings.TrimSuffix(root, "/") + "/" + src
}

// Lookup resolves a 0-based generated line and column. On a line with
// mappings it returns the nearest mapping at or before col on that same line;
// when col precedes the first mapping of the line it returns that first
// mapping. It never reads a neighbouring line. ok is false when the line has
// no mappings or the chosen segment maps nowhere.
func (m *Map) Lookup(line, col int) (Mapping, bool) { return m.lookup(line, col, false) }

// LookupExact is Lookup restricted to a segment that starts exactly at the
// given generated position.
func (m *Map) LookupExact(line, col int) (Mapping, bool) { return m.lookup(line, col, true) }

func (m *Map) lookup(line, col int, exact bool) (Mapping, bool) {
	if line < 0 || col < 0 || line > maxVLQ {
		return Mapping{}, false
	}
	l, c := int32(line), int32(min(col, maxVLQ))
	// First segment strictly after (l, c).
	i := sort.Search(len(m.segs), func(i int) bool {
		s := m.segs[i]
		return s.genLine > l || (s.genLine == l && s.genCol > c)
	})
	var s segment
	switch {
	case i > 0 && m.segs[i-1].genLine == l:
		s = m.segs[i-1]
	case exact:
		return Mapping{}, false
	case i < len(m.segs) && m.segs[i].genLine == l:
		s = m.segs[i]
	default:
		return Mapping{}, false
	}
	if s.src < 0 || (exact && s.genCol != c) {
		return Mapping{}, false
	}
	out := Mapping{Source: m.Sources[s.src], Line: int(s.srcLine) + 1, Col: int(s.srcCol)}
	if s.name >= 0 {
		out.Name, out.HasName = m.Names[s.name], true
	}
	return out, true
}

// SourceContext returns up to n lines before and after the 1-based line of
// the named source (as in Mapping.Source), and the line itself. It returns an
// empty cur and nil slices when the map carries no content for src or the
// line is out of range.
func (m *Map) SourceContext(src string, line, n int) (pre []string, cur string, post []string) {
	idx, ok := m.byName[src]
	if !ok || m.SourcesContent[idx] == nil || line < 1 || n < 0 {
		return nil, "", nil
	}
	lines := strings.Split(*m.SourcesContent[idx], "\n")
	if line > len(lines) {
		return nil, "", nil
	}
	clean := func(s string) string { return strings.TrimSuffix(s, "\r") }
	for i := max(0, line-1-n); i < line-1; i++ {
		pre = append(pre, clean(lines[i]))
	}
	cur = clean(lines[line-1])
	for i := line; i < len(lines) && i < line+n; i++ {
		post = append(post, clean(lines[i]))
	}
	return pre, cur, post
}
