package server

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// validateHTMXResponseStructure validates that an HTMX response has the correct structure
// for the specified swap type. This helps catch structural issues that would cause
// HTMX swap errors in the browser.
//
// Parameters:
//   - body: The response body string
//   - swapType: The HTMX swap type (e.g., "outerHTML", "innerHTML", "beforeend")
//   - targetID: The target element ID (for validating OOB swaps)
//
// Returns an error if the structure is invalid, nil otherwise.
func validateHTMXResponseStructure(body string, swapType string, targetID string) error {
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to parse HTML: %w", err)
	}

	// For outerHTML swap, the response must have exactly one root element for the main swap
	// OOB swaps (hx-swap-oob) are processed separately and don't count toward the main swap
	if swapType == "outerHTML" {
		// Find the body element (HTML parser always creates html/head/body structure)
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

		// Count direct element children of body that are NOT OOB swaps
		mainSwapElements := 0
		if bodyNode != nil {
			for child := bodyNode.FirstChild; child != nil; child = child.NextSibling {
				if child.Type == html.ElementNode {
					// Check if this element has hx-swap-oob attribute
					hasOOB := false
					for _, attr := range child.Attr {
						if attr.Key == "hx-swap-oob" {
							hasOOB = true
							break
						}
					}
					// Only count elements that are NOT OOB swaps
					if !hasOOB {
						mainSwapElements++
					}
				}
			}
		} else {
			// No body found - might be a fragment. Count non-OOB elements
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
							// Direct element child of document (fragment)
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

	// Validate OOB swaps have valid target IDs
	oobElements := findElementsWithAttribute(doc, "hx-swap-oob")
	for _, elem := range oobElements {
		if elem == nil {
			continue
		}
		id, hasID := getAttribute(elem, "id")
		if !hasID || id == "" {
			return fmt.Errorf("OOB swap element missing required 'id' attribute")
		}
	}

	return nil
}

// findElementsWithAttribute finds all elements with a specific attribute
func findElementsWithAttribute(n *html.Node, attrName string) []*html.Node {
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

// TestValidateHTMXResponseStructure tests the validation helper.
// Note: HTML string literals are kept inline here (rather than using templates) for test clarity and readability.
// These are minimal test fixtures used only in assertions, not production code, so inline strings are appropriate.
func TestValidateHTMXResponseStructure(t *testing.T) {
	t.Run("valid single root element for outerHTML", func(t *testing.T) {
		body := `<div id="config-success-message"></div>`
		err := validateHTMXResponseStructure(body, "outerHTML", "")
		if err != nil {
			t.Errorf("Expected no error for valid single root, got: %v", err)
		}
	})

	t.Run("invalid multiple root elements for outerHTML", func(t *testing.T) {
		body := `<div id="config-success-message"></div><div id="config-error-message"></div>`
		err := validateHTMXResponseStructure(body, "outerHTML", "")
		if err == nil {
			t.Error("Expected error for multiple root elements, got nil")
		}
		if !strings.Contains(err.Error(), "one root element") {
			t.Errorf("Expected error about root elements, got: %v", err)
		}
	})

	t.Run("valid OOB swap with ID and main swap element", func(t *testing.T) {
		// OOB swaps are processed separately, main swap needs one root
		body := `<div id="config-success-message" hx-swap-oob="outerHTML"></div><div id="config-success-message"></div>`
		err := validateHTMXResponseStructure(body, "outerHTML", "config-success-message")
		if err != nil {
			t.Errorf("Expected no error for valid OOB swap with main element, got: %v", err)
		}
	})

	t.Run("invalid OOB swap without ID", func(t *testing.T) {
		body := `<div hx-swap-oob="outerHTML"></div><div id="config-success-message"></div>`
		err := validateHTMXResponseStructure(body, "outerHTML", "")
		if err == nil {
			t.Error("Expected error for OOB swap without ID, got nil")
		}
		if !strings.Contains(err.Error(), "missing required 'id' attribute") {
			t.Errorf("Expected error about missing ID, got: %v", err)
		}
	})
}
