package ctdf

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// StripHTML removes markup from alert content while keeping readable text.
func (a *ServiceAlert) StripHTML() {
	a.Title = stripHTML(a.Title)
	a.Text = stripHTML(a.Text)
}

func stripHTML(value string) string {
	if value == "" {
		return value
	}

	root := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(value), root)
	if err != nil {
		return value
	}

	var text strings.Builder
	var appendText func(*html.Node)
	appendText = func(node *html.Node) {
		if node.Type == html.ElementNode {
			switch node.Data {
			case "script", "style":
				return
			case "address", "article", "blockquote", "br", "div", "footer", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "li", "ol", "p", "pre", "section", "table", "td", "th", "tr", "ul":
				text.WriteByte(' ')
			}
		}
		if node.Type == html.TextNode {
			text.WriteString(node.Data)
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			appendText(child)
		}
		if node.Type == html.ElementNode {
			switch node.Data {
			case "address", "article", "blockquote", "br", "div", "footer", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "li", "ol", "p", "pre", "section", "table", "td", "th", "tr", "ul":
				text.WriteByte(' ')
			}
		}
	}
	for _, node := range nodes {
		appendText(node)
	}

	return strings.Join(strings.Fields(text.String()), " ")
}
