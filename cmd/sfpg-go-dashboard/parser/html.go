package parser

import (
	"io"
	"strings"

	"golang.org/x/net/html"
)

// parseHTML parses an io.Reader into an html.Node.
func parseHTML(r io.Reader) (*html.Node, error) {
	return html.Parse(r)
}

// findElementByID returns the first element whose id attribute equals id.
func findElementByID(n *html.Node, id string) *html.Node {
	return findElement(n, func(n *html.Node) bool {
		return getAttr(n, "id") == id
	})
}

// findElement returns the first element node in n's subtree matching the predicate.
func findElement(n *html.Node, match func(*html.Node) bool) *html.Node {
	if n == nil {
		return nil
	}
	if n.Type == html.ElementNode && match(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findElement(c, match); found != nil {
			return found
		}
	}
	return nil
}

// getAttr returns the value of an attribute, or an empty string if not found.
func getAttr(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

// getTextContent returns the concatenated text of all text nodes within n.
func getTextContent(n *html.Node) string {
	if n == nil {
		return ""
	}
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}
