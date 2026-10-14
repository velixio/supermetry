package pixels

import (
	"bytes"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const (
	labelColor   = "#1c1a2b"
	defaultColor = "#5b3cf0"
	charWidth    = 7
	padding      = 6
	markWidth    = 14
)

var hexColor = regexp.MustCompile(`^[0-9a-fA-F]{6}$`)

func FormatCount(n int64) string {
	units := []struct {
		div    int64
		suffix string
	}{{1_000_000_000, "B"}, {1_000_000, "M"}, {1_000, "k"}}
	for i, u := range units {
		if n < u.div {
			continue
		}
		tenths := (n*10 + u.div/2) / u.div
		if tenths >= 10_000 && i > 0 {
			return "1" + units[i-1].suffix
		}
		if tenths%10 == 0 {
			return strconv.FormatInt(tenths/10, 10) + u.suffix
		}
		return strconv.FormatInt(tenths/10, 10) + "." + strconv.FormatInt(tenths%10, 10) + u.suffix
	}
	return strconv.FormatInt(n, 10)
}

func ParseBadgeQuery(q url.Values) (style, color string) {
	style = q.Get("style")
	if style != "flat-square" {
		style = "flat"
	}
	if c := q.Get("color"); hexColor.MatchString(c) {
		color = strings.ToLower(c)
	}
	return style, color
}

type BadgeOptions struct {
	Label   string
	Count   int64
	Style   string
	Color   string
	Branded bool
}

func RenderBadge(o BadgeOptions) []byte {
	value := FormatCount(o.Count)
	label := html.EscapeString(o.Label)
	left := len([]rune(o.Label))*charWidth + 2*padding
	textX := padding
	if o.Branded {
		left += markWidth + 2
		textX += markWidth + 2
	}
	right := len(value)*charWidth + 2*padding
	width := left + right
	fill := defaultColor
	if o.Color != "" {
		fill = "#" + o.Color
	}
	rx := ""
	if o.Style != "flat-square" {
		rx = ` rx="3"`
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20" role="img" aria-label="%s: %s">`, width, label, value)
	fmt.Fprintf(&b, `<title>%s: %s</title><clipPath id="r"><rect width="%d" height="20"%s/></clipPath><g clip-path="url(#r)">`, label, value, width, rx)
	fmt.Fprintf(&b, `<rect width="%d" height="20" fill="%s"/><rect x="%d" width="%d" height="20" fill="%s"/></g>`, left, labelColor, left, right, fill)
	if o.Branded {
		b.WriteString(`<g id="sm-mark" fill="#fff"><rect x="6" y="11" width="3" height="4"/><rect x="10" y="8" width="3" height="7"/><rect x="14" y="5" width="3" height="10"/></g>`)
	}
	fmt.Fprintf(&b, `<g fill="#fff" font-family="Verdana,DejaVu Sans,sans-serif" font-size="11"><text x="%d" y="14">%s</text><text x="%d" y="14">%s</text></g></svg>`, textX, label, left+padding, value)
	return b.Bytes()
}
