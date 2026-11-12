package core

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestPublicHTMLViews(t *testing.T) {
	for _, route := range []string{"token", "readable"} {
		for _, view := range []string{"latest", "list", "ref", "id"} {
			t.Run(route+"/"+view, func(t *testing.T) {
				a := testApp(t, "primary")
				account := accountFixture(t, a, "html-page")
				link := linkFixture(t, a, account.ID, "child@example.net")
				fake := &paginationMail{}
				a.mail.backend = fake
				path := "/m/" + link.Token
				if route == "readable" {
					path = "/show/p0/" + account.PublicCode + "/child"
				}
				switch view {
				case "list":
					path += "?list=1"
				case "ref":
					ref := signRef(a.key, MessageRef{AccountID: account.ID, Protocol: "graph", Folder: "inbox", ID: "7"})
					path += "?list=1&ref=" + url.QueryEscape(ref)
				case "id":
					path += "?id=7"
				}
				w := request(t, a.Handler(), http.MethodGet, path, nil, nil, "")
				if w.Code != http.StatusOK {
					t.Fatalf("page returned %d: %s", w.Code, w.Body.String())
				}
				doc, err := html.Parse(strings.NewReader(w.Body.String()))
				if err != nil {
					t.Fatal(err)
				}
				if headings := publicHTMLNodes(doc, "h1", ""); len(headings) != 1 || publicHTMLText(headings[0]) != "最新邮件" {
					t.Fatal("public page should retain the latest-mail heading")
				}
				if view == "list" {
					items := publicHTMLNodes(doc, "details", "mail-item")
					if len(items) != 20 {
						t.Fatalf("list should render 20 accordion summaries, got %d", len(items))
					}
					if first, last := publicHTMLText(publicHTMLNodes(items[0], "summary", "")[0]), publicHTMLText(publicHTMLNodes(items[19], "summary", "")[0]); first != "subject-00" || last != "subject-19" {
						t.Fatalf("list rendered the wrong page: first=%q last=%q", first, last)
					}
					if len(publicHTMLNodes(doc, "section", "mail-single")) != 0 || fake.details.Load() != 0 {
						t.Fatalf("list must not load a message body on the server: details=%d", fake.details.Load())
					}
					for _, frame := range publicHTMLNodes(doc, "iframe", "mail-frame") {
						if publicHTMLAttr(frame, "srcdoc") != "" {
							t.Fatal("list body should be loaded only when a message is opened")
						}
					}
					if main := publicHTMLNodes(doc, "main", "")[0]; publicHTMLAttr(main, "data-has-more") != "true" || publicHTMLAttr(main, "data-cursor") == "" {
						t.Fatal("list lost its continuation cursor")
					}
					return
				}
				if len(publicHTMLNodes(doc, "section", "mail-single")) != 1 || len(publicHTMLNodes(doc, "details", "mail-item")) != 0 {
					t.Fatal("single view should render one mail card without an accordion list")
				}
				bodies := publicHTMLNodes(doc, "pre", "mail-text")
				if len(bodies) != 1 || publicHTMLText(bodies[0]) != "body" || fake.details.Load() != 1 {
					t.Fatalf("single view must load exactly one complete body: bodies=%d details=%d", len(bodies), fake.details.Load())
				}
				more := false
				for _, anchor := range publicHTMLNodes(doc, "a", "button") {
					if publicHTMLText(anchor) == "查看更多" && publicHTMLAttr(anchor, "href") == "?list=1" {
						more = true
					}
				}
				if !more {
					t.Fatal("single view should offer the full mail list")
				}
				if view == "ref" || view == "id" {
					if headings := publicHTMLNodes(doc, "h2", ""); len(headings) != 1 || publicHTMLText(headings[0]) != "subject-7" {
						t.Fatal("explicit message query did not retain the selected message")
					}
				}
			})
		}
	}
}

func publicHTMLNodes(root *html.Node, tag, class string) []*html.Node {
	var out []*html.Node
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == tag && (class == "" || strings.Contains(" "+publicHTMLAttr(n, "class")+" ", " "+class+" ")) {
			out = append(out, n)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(root)
	return out
}

func publicHTMLAttr(n *html.Node, name string) string {
	for _, attr := range n.Attr {
		if attr.Key == name {
			return attr.Val
		}
	}
	return ""
}

func publicHTMLText(n *html.Node) string {
	var text strings.Builder
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.TextNode {
			text.WriteString(n.Data)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(n)
	return strings.TrimSpace(text.String())
}
