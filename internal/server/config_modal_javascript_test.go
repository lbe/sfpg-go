package server

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// TestConfigModal_JavaScript_RendersCorrectly verifies that the JavaScript in the admin tab
// renders correctly without HTML entity encoding issues (e.g., < should not become &lt;)
func TestConfigModal_JavaScript_RendersCorrectly(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()
	app.setDB()
	if err := app.loadConfig(); err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	server := httptest.NewServer(app.getRouter())
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("Failed to create cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	authCookie := MakeAuthCookie(t, app)
	serverURL, _ := url.Parse(server.URL)
	jar.SetCookies(serverURL, []*http.Cookie{authCookie})

	resp, err := client.Get(server.URL + "/config")
	if err != nil {
		t.Fatalf("GET /config failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", resp.StatusCode)
	}

	// Parse HTML to verify its content
	doc, err := html.Parse(resp.Body)
	if err != nil {
		t.Fatalf("Failed to parse HTML: %v", err)
	}

	// Find the script block with type="text/javascript"
	var scriptNode *html.Node
	var findScript func(*html.Node)
	findScript = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "script" {
			for _, attr := range n.Attr {
				if attr.Key == "type" && attr.Val == "text/javascript" {
					scriptNode = n
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if scriptNode == nil {
				findScript(c)
			}
		}
	}
	findScript(doc)

	if scriptNode == nil {
		t.Fatal("JavaScript script block not found in rendered output")
	}

	// Get the text content of the script block
	scriptContent := getTextContent(scriptNode)

	// Verify the script content contains the regex without HTML entities
	// We specifically check that the regex definition doesn't contain encoded entities
	if strings.Contains(scriptContent, "&lt;") {
		t.Error("Script content contains HTML entity &lt; - should contain < character")
	}

	// Verify regex parts are present
	if !strings.Contains(scriptContent, "const regex =") || !strings.Contains(scriptContent, "(?=.*[a-z])") {
		t.Error("Regex pattern not found in script content")
	}

	// Verify function definitions are present
	if !strings.Contains(scriptContent, "function checkPasswordValid") {
		t.Error("checkPasswordValid function not found in script content")
	}
	if !strings.Contains(scriptContent, "function validateNewPassword") {
		t.Error("validateNewPassword function not found in script content")
	}
	if !strings.Contains(scriptContent, "function validateConfirmPassword") {
		t.Error("validateConfirmPassword function not found in script content")
	}
}
