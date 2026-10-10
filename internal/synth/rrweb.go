package synth

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"strings"
)

type demoRRWebEvent struct {
	Type      int   `json:"type"`
	Data      any   `json:"data"`
	Timestamp int64 `json:"timestamp"`
}

type demoRRWebNode struct {
	Type        int               `json:"type"`
	ID          int               `json:"id"`
	Name        string            `json:"name,omitempty"`
	PublicID    string            `json:"publicId,omitempty"`
	SystemID    string            `json:"systemId,omitempty"`
	TagName     string            `json:"tagName,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	TextContent string            `json:"textContent,omitempty"`
	ChildNodes  []demoRRWebNode   `json:"childNodes,omitempty"`
}

type demoSnapshot struct {
	nextID int
	bodyID int
}

// DemoRecording returns a gzip member containing newline-delimited rrweb events.
func DemoRecording(visit DemoVisit) []byte {
	var plain bytes.Buffer
	encoder := json.NewEncoder(&plain)
	nextID := 1
	activeBodyID := 1
	for _, hit := range visit.Hits {
		timestamp := visit.Start.Add(hit.Offset).UnixMilli()
		switch hit.Kind {
		case 0:
			host := hostForPath(hit.Path)
			if visit.Host != "" && !strings.HasPrefix(hit.Path, "/docs/") {
				host = visit.Host
			}
			snapshot := newDemoSnapshot(hit.Path, &nextID)
			activeBodyID = snapshot.bodyID
			encodeDemoEvent(encoder, demoRRWebEvent{Type: 4, Timestamp: timestamp, Data: map[string]any{
				"href": "https://" + host + hit.Path, "width": max(320, visit.ScreenW), "height": 900,
			}})
			encodeDemoEvent(encoder, demoRRWebEvent{Type: 2, Timestamp: timestamp, Data: map[string]any{
				"node": snapshot.node, "initialOffset": map[string]int{"top": 0, "left": 0},
			}})
			if len(hit.Clicks) == 0 {
				encodeDemoEvent(encoder, demoRRWebEvent{Type: 3, Timestamp: timestamp, Data: map[string]any{
					"source": 1, "positions": []map[string]int{{"x": max(160, visit.ScreenW/2), "y": 320, "id": snapshot.bodyID, "timeOffset": 0}},
				}})
			}
			for _, click := range hit.Clicks {
				encodeDemoEvent(encoder, demoRRWebEvent{Type: 3, Timestamp: timestamp, Data: map[string]any{
					"source": 1, "positions": []map[string]int{{"x": click[0], "y": click[1], "id": snapshot.bodyID, "timeOffset": 0}},
				}})
				encodeDemoEvent(encoder, demoRRWebEvent{Type: 3, Timestamp: timestamp, Data: map[string]any{
					"source": 2, "type": 2, "x": click[0], "y": click[1], "id": snapshot.bodyID,
				}})
			}
		case 2:
			encodeDemoEvent(encoder, demoRRWebEvent{Type: 3, Timestamp: timestamp, Data: map[string]any{
				"source": 3, "id": activeBodyID, "x": 0, "y": int(hit.ScrollPct) * 12,
			}})
		}
	}

	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	_, _ = zw.Write(plain.Bytes())
	_ = zw.Close()
	return compressed.Bytes()
}

type demoSnapshotResult struct {
	node   demoRRWebNode
	bodyID int
}

func newDemoSnapshot(path string, nextID *int) demoSnapshotResult {
	state := demoSnapshot{nextID: *nextID}
	newNode := func(nodeType int) demoRRWebNode {
		node := demoRRWebNode{Type: nodeType, ID: state.nextID}
		state.nextID++
		return node
	}
	text := func(value string) demoRRWebNode {
		node := newNode(3)
		node.TextContent = value
		return node
	}
	element := func(tag string, attributes map[string]string) demoRRWebNode {
		node := newNode(2)
		node.TagName = tag
		node.Attributes = attributes
		return node
	}

	title, heading := demoPageCopy(path)
	document := newNode(0)
	doctype := newNode(1)
	doctype.Name = "html"
	html := element("html", map[string]string{"lang": "en"})
	head := element("head", map[string]string{})
	meta := element("meta", map[string]string{"charset": "utf-8"})
	titleNode := element("title", map[string]string{})
	titleNode.ChildNodes = []demoRRWebNode{text(title)}
	style := element("style", map[string]string{})
	style.ChildNodes = []demoRRWebNode{text("body{font-family:system-ui;margin:0;color:#172033}header,main,footer{padding:24px 8%}nav a{margin-right:20px}.hero{padding:72px 0}button{padding:12px 20px}")}
	head.ChildNodes = []demoRRWebNode{meta, titleNode, style}
	body := element("body", map[string]string{})
	state.bodyID = body.ID
	header := element("header", map[string]string{})
	nav := element("nav", map[string]string{"aria-label": "Primary"})
	for _, link := range [][2]string{{"/", "Acme Cloud"}, {"/features/analytics", "Features"}, {"/pricing", "Pricing"}, {"/docs/getting-started", "Docs"}} {
		a := element("a", map[string]string{"href": link[0]})
		a.ChildNodes = []demoRRWebNode{text(link[1])}
		nav.ChildNodes = append(nav.ChildNodes, a)
	}
	header.ChildNodes = []demoRRWebNode{nav}
	main := element("main", map[string]string{})
	hero := element("section", map[string]string{"class": "hero"})
	h1 := element("h1", map[string]string{})
	h1.ChildNodes = []demoRRWebNode{text(heading)}
	p := element("p", map[string]string{})
	p.ChildNodes = []demoRRWebNode{text("Understand your product with fast, privacy-friendly analytics.")}
	button := element("button", map[string]string{"type": "button"})
	button.ChildNodes = []demoRRWebNode{text("Start free")}
	hero.ChildNodes = []demoRRWebNode{h1, p, button}
	main.ChildNodes = []demoRRWebNode{hero}
	footer := element("footer", map[string]string{})
	footer.ChildNodes = []demoRRWebNode{text("Acme Cloud · Fictional demo company")}
	body.ChildNodes = []demoRRWebNode{header, main, footer}
	html.ChildNodes = []demoRRWebNode{head, body}
	document.ChildNodes = []demoRRWebNode{doctype, html}
	*nextID = state.nextID
	return demoSnapshotResult{node: document, bodyID: state.bodyID}
}

func demoPageCopy(path string) (title, heading string) {
	switch {
	case path == "/":
		return "Acme Cloud", "Analytics that make sense"
	case path == "/pricing":
		return "Pricing · Acme Cloud", "Simple plans for growing teams"
	case strings.HasPrefix(path, "/features/"):
		return "Features · Acme Cloud", "See the whole customer journey"
	case strings.HasPrefix(path, "/blog/"):
		return "Acme Cloud Blog", "Ideas for better product decisions"
	case strings.HasPrefix(path, "/docs/"):
		return "Documentation · Acme Cloud", "Build with Acme Cloud"
	case path == "/old-pricing":
		return "Not found · Acme Cloud", "This page has moved"
	default:
		return "Acme Cloud", "Welcome to Acme Cloud"
	}
}

func encodeDemoEvent(encoder *json.Encoder, event demoRRWebEvent) {
	_ = encoder.Encode(event)
}
