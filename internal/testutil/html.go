package testutil

import (
	"io"
	"strings"

	"golang.org/x/net/html"
)

// FindElementByID finds an element by its id attribute.
func FindElementByID(n *html.Node, id string) *html.Node {
	return FindElement(n, func(n *html.Node) bool {
		return GetAttr(n, "id") == id
	})
}

// FindElementByClass finds the first element containing the given class.
func FindElementByClass(n *html.Node, class string) *html.Node {
	return FindElement(n, func(n *html.Node) bool {
		classes := strings.Fields(GetAttr(n, "class"))
		for _, c := range classes {
			if c == class {
				return true
			}
		}
		return false
	})
}

// FindElementByTag finds the first element with the given tag name.
func FindElementByTag(n *html.Node, tag string) *html.Node {
	return FindElement(n, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == tag
	})
}

// FindElement traverses the tree and returns the first node matching the predicate.
func FindElement(n *html.Node, match func(*html.Node) bool) *html.Node {
	if n == nil {
		return nil
	}
	if n.Type == html.ElementNode && match(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := FindElement(c, match); found != nil {
			return found
		}
	}
	return nil
}

// FindAllElements returns all nodes matching the predicate.
func FindAllElements(n *html.Node, match func(*html.Node) bool) []*html.Node {
	var results []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && match(n) {
			results = append(results, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return results
}

// GetAttr returns the value of an attribute, or empty string if not found.
func GetAttr(n *html.Node, key string) string {
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

// GetTextContent returns all text content within a node.
func GetTextContent(n *html.Node) string {
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

// ParseHTML parses an io.Reader into an html.Node.
func ParseHTML(r io.Reader) (*html.Node, error) {
	return html.Parse(r)
}
