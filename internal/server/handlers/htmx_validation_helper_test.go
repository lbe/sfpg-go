package handlers

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func validateHTMXResponseStructure(body string, swapType string, targetID string) error {
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to parse HTML: %w", err)
	}

	if swapType == "outerHTML" {
		var bodyNode *html.Node
		var findBody func(*html.Node)
		findBody = func(n *html.Node) {
			if n.Type == html.ElementNode && n.Data == "body" {
				bodyNode = n
				return
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if bodyNode == nil {
					findBody(c)
				}
			}
		}
		findBody(doc)

		mainSwapElements := 0
		if bodyNode != nil {
			for child := bodyNode.FirstChild; child != nil; child = child.NextSibling {
				if child.Type == html.ElementNode {
					hasOOB := false
					for _, attr := range child.Attr {
						if attr.Key == "hx-swap-oob" {
							hasOOB = true
							break
						}
					}
					if !hasOOB {
						mainSwapElements++
					}
				}
			}
		} else {
			countMainSwap := func(n *html.Node) int {
				count := 0
				if n.Type == html.DocumentNode {
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						if c.Type == html.ElementNode && c.Data == "html" {
							for hc := c.FirstChild; hc != nil; hc = hc.NextSibling {
								if hc.Type == html.ElementNode && hc.Data == "body" {
									for child := hc.FirstChild; child != nil; child = child.NextSibling {
										if child.Type == html.ElementNode {
											hasOOB := false
											for _, attr := range child.Attr {
												if attr.Key == "hx-swap-oob" {
													hasOOB = true
													break
												}
											}
											if !hasOOB {
												count++
											}
										}
									}
								}
							}
						} else if c.Type == html.ElementNode && c.Data != "html" {
							hasOOB := false
							for _, attr := range c.Attr {
								if attr.Key == "hx-swap-oob" {
									hasOOB = true
									break
								}
							}
							if !hasOOB {
								count++
							}
						}
					}
				}
				return count
			}
			mainSwapElements = countMainSwap(doc)
		}

		if mainSwapElements != 1 {
			return fmt.Errorf("HTMX outerHTML swap requires exactly one root element for main swap (excluding OOB swaps), found %d", mainSwapElements)
		}
	}

	oobElements := findHTMXElementsWithAttribute(doc, "hx-swap-oob")
	for _, elem := range oobElements {
		if elem == nil {
			continue
		}
		id, hasID := getHTMXHTMLAttribute(elem, "id")
		if !hasID || id == "" {
			return fmt.Errorf("OOB swap element missing required 'id' attribute")
		}
	}

	return nil
}

func findHTMXElementsWithAttribute(n *html.Node, attrName string) []*html.Node {
	var results []*html.Node
	var find func(*html.Node)
	find = func(node *html.Node) {
		if node.Type == html.ElementNode {
			for _, a := range node.Attr {
				if a.Key == attrName {
					results = append(results, node)
					break
				}
			}
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(n)
	return results
}

func getHTMXHTMLAttribute(n *html.Node, key string) (string, bool) {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val, true
		}
	}
	return "", false
}

func TestValidateHTMXResponseStructure(t *testing.T) {
	body := `<div id="config-success-message">ok</div>`
	if err := validateHTMXResponseStructure(body, "outerHTML", "config-success-message"); err != nil {
		t.Fatalf("validateHTMXResponseStructure: %v", err)
	}
}
