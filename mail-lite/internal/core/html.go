package core

import (
	"golang.org/x/net/html"
	"io"
	"strings"
)

var emailTags = map[string]bool{}

func init() {
	for _, tag := range strings.Fields("div span p br hr table thead tbody tfoot tr td th colgroup col caption b strong em i u s del blockquote pre code ul ol li dl dt dd h1 h2 h3 h4 h5 h6 a img style center font") {
		emailTags[tag] = true
	}
}

// Keep formatting while dropping active elements and navigation, even inside
// the sandbox. The surrounding document separately enforces a restrictive CSP.
func sanitizeEmail(source string) string {
	z := html.NewTokenizer(strings.NewReader(source))
	var out strings.Builder
	skip := ""
	for {
		kind := z.Next()
		if kind == html.ErrorToken {
			if z.Err() == io.EOF {
				return out.String()
			}
			return ""
		}
		token := z.Token()
		if skip != "" {
			if kind == html.EndTagToken && token.Data == skip {
				skip = ""
			}
			continue
		}
		switch kind {
		case html.StartTagToken, html.SelfClosingTagToken:
			if !emailTags[token.Data] {
				switch token.Data {
				case "script", "iframe", "object", "svg", "math", "textarea", "select", "video", "audio":
					if kind == html.StartTagToken {
						skip = token.Data
					}
				}
				continue
			}
			attrs := []html.Attribute{}
			for _, a := range token.Attr {
				if a.Namespace != "" {
					continue
				}
				switch a.Key {
				case "style", "class", "id", "align", "valign", "bgcolor", "width", "height", "cellpadding", "cellspacing", "border", "colspan", "rowspan", "alt", "title", "dir", "color", "face", "size", "lang":
					attrs = append(attrs, a)
				case "src":
					v := strings.ToLower(strings.TrimSpace(a.Val))
					if token.Data == "img" && (strings.HasPrefix(v, "data:image/png;base64,") || strings.HasPrefix(v, "data:image/jpeg;base64,") || strings.HasPrefix(v, "data:image/gif;base64,") || strings.HasPrefix(v, "data:image/webp;base64,")) {
						attrs = append(attrs, a)
					}
				case "href":
					if token.Data == "a" && strings.HasPrefix(a.Val, "#") {
						attrs = append(attrs, a)
					}
				}
			}
			token.Attr = attrs
			out.WriteString(token.String())
		case html.EndTagToken:
			if emailTags[token.Data] {
				out.WriteString(token.String())
			}
		case html.TextToken:
			out.WriteString(token.String())
		}
	}
}

func emailDocument(m Message) string {
	return `<!doctype html><html><head><meta name="viewport" content="width=device-width, initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data:; form-action 'none'; base-uri 'none'"><style>body{font:15px/1.7 system-ui;color:#20242c;background:white;padding:12px;overflow-wrap:anywhere}img,table{max-width:100%}</style></head><body>` + strings.TrimSpace(m.HTML) + `</body></html>`
}
