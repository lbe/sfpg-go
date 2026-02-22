// Package server_test contains tests for configuration help text and example value features.
// These tests verify database schema, retrieval, UI display, tooltips, and example value rendering.
package server

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
	"golang.org/x/net/html"

	"github.com/lbe/sfpg-go/migrations"
)

// TestConfigHelp_DatabaseSchema_HelpTextColumn verifies that the help_text column
// exists in the config table after migration 007 is applied.
func TestConfigHelp_DatabaseSchema_HelpTextColumn(t *testing.T) {
	// Setup test database
	dbfile := filepath.Join(t.TempDir(), "test_help_text.db")
	db, err := sql.Open("sqlite3", dbfile)
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	defer db.Close()

	// Run migrations
	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		t.Fatalf("failed to create sqlite driver: %v", err)
	}

	source, err := iofs.New(migrations.FS, "migrations")
	if err != nil {
		t.Fatalf("failed to create migration source: %v", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, "sqlite", driver)
	if err != nil {
		t.Fatalf("failed to create migrate instance: %v", err)
	}
	defer m.Close()

	if upErr := m.Up(); upErr != nil && upErr != migrate.ErrNoChange {
		t.Fatalf("failed to run migrations: %v", upErr)
	}

	// Check that help_text column exists
	var columnExists bool
	err = db.QueryRow(`
		SELECT COUNT(*) > 0 
		FROM pragma_table_info('config') 
		WHERE name = 'help_text'
	`).Scan(&columnExists)
	if err != nil {
		t.Fatalf("failed to query table info: %v", err)
	}

	if !columnExists {
		t.Error("help_text column does not exist in config table")
	}
}

// TestConfigHelp_DatabaseSchema_ExampleValueColumn verifies that the example_value column
// exists in the config table after migration 007 is applied.
// exists in the config table after migration.
func TestConfigHelp_DatabaseSchema_ExampleValueColumn(t *testing.T) {
	// Setup test database
	dbfile := filepath.Join(t.TempDir(), "test_example_value.db")
	db, err := sql.Open("sqlite3", dbfile)
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	defer db.Close()

	// Run migrations
	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		t.Fatalf("failed to create sqlite driver: %v", err)
	}

	source, err := iofs.New(migrations.FS, "migrations")
	if err != nil {
		t.Fatalf("failed to create migration source: %v", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, "sqlite", driver)
	if err != nil {
		t.Fatalf("failed to create migrate instance: %v", err)
	}
	defer m.Close()

	if upErr := m.Up(); upErr != nil && upErr != migrate.ErrNoChange {
		t.Fatalf("failed to run migrations: %v", upErr)
	}

	// Check that example_value column exists
	var columnExists bool
	err = db.QueryRow(`
		SELECT COUNT(*) > 0 
		FROM pragma_table_info('config') 
		WHERE name = 'example_value'
	`).Scan(&columnExists)
	if err != nil {
		t.Fatalf("failed to query table info: %v", err)
	}

	if !columnExists {
		t.Error("example_value column does not exist in config table")
	}
}

// TestConfigHelp_RetrieveHelpText verifies that help text can be retrieved from the database
// and is correctly associated with configuration keys.
func TestConfigHelp_RetrieveHelpText(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	// Insert a config setting with help text
	ctx := context.Background()
	_, err = cpcRw.Conn.ExecContext(ctx, `
		INSERT INTO config (key, value, help_text, example_value, created_at, updated_at)
		VALUES (?, ?, ?, ?, UNIXEPOCH('now'), UNIXEPOCH('now'))
	`, "test_setting", "test_value", "This is help text for the test setting", "example_value")
	if err != nil {
		t.Fatalf("Failed to insert test config: %v", err)
	}

	// Retrieve help text
	var helpText, exampleValue string
	err = cpcRw.Conn.QueryRowContext(ctx, `
		SELECT help_text, example_value 
		FROM config 
		WHERE key = ?
	`, "test_setting").Scan(&helpText, &exampleValue)
	if err != nil {
		t.Fatalf("Failed to retrieve help text: %v", err)
	}

	if helpText != "This is help text for the test setting" {
		t.Errorf("Expected help text 'This is help text for the test setting', got '%s'", helpText)
	}

	if exampleValue != "example_value" {
		t.Errorf("Expected example value 'example_value', got '%s'", exampleValue)
	}
}

// TestConfigHelp_DisplayedInUI verifies that help text is displayed in the configuration UI
// using HTML parsing to check for help text in label-text-alt spans.
func TestConfigHelp_DisplayedInUI(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	// Set up authenticated session
	req := httptest.NewRequest("GET", "/config", nil)
	w := httptest.NewRecorder()

	session, err := app.store.Get(req, "session-name")
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}
	session.Values["authenticated"] = true
	if saveErr := session.Save(req, w); saveErr != nil {
		t.Fatalf("failed to save session: %v", saveErr)
	}

	// Add help text to a config setting
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	ctx := context.Background()
	_, err = cpcRw.Conn.ExecContext(ctx, `
		INSERT OR REPLACE INTO config (key, value, help_text, example_value, created_at, updated_at)
		VALUES (?, ?, ?, ?, UNIXEPOCH('now'), UNIXEPOCH('now'))
	`, "listener_port", "8081", "The port number the server listens on (1-65535)", "8081")
	if err != nil {
		t.Fatalf("Failed to insert help text: %v", err)
	}

	// Reload config to include help text
	if loadErr := app.loadConfig(); loadErr != nil {
		t.Fatalf("Failed to reload config: %v", loadErr)
	}

	// Make authenticated request
	req.Header.Set("Cookie", w.Header().Get("Set-Cookie"))
	w2 := httptest.NewRecorder()
	app.configHandlers.ConfigGet(w2, req)

	if w2.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w2.Code)
	}

	// Parse HTML response
	doc, err := html.Parse(strings.NewReader(w2.Body.String()))
	if err != nil {
		t.Fatalf("Failed to parse HTML: %v", err)
	}

	// Find help text element (should be in a tooltip or help icon)
	// This will depend on the UI implementation, but we expect help text to be present
	helpTextFound := false
	var searchHelpText func(*html.Node)
	searchHelpText = func(n *html.Node) {
		if n.Type == html.TextNode {
			if strings.Contains(n.Data, "The port number the server listens on") {
				helpTextFound = true
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if !helpTextFound {
				searchHelpText(c)
			}
		}
	}
	searchHelpText(doc)

	if !helpTextFound {
		t.Error("Help text 'The port number the server listens on' not found in HTML response")
	}
}

// TestConfigHelp_TooltipsWork verifies that tooltips display help text on hover
// by checking the data-tip attribute value in the rendered HTML.
func TestConfigHelp_TooltipsWork(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	// Set up authenticated session
	req := httptest.NewRequest("GET", "/config", nil)
	w := httptest.NewRecorder()

	session, err := app.store.Get(req, "session-name")
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}
	session.Values["authenticated"] = true
	if saveErr := session.Save(req, w); saveErr != nil {
		t.Fatalf("failed to save session: %v", saveErr)
	}

	// Add help text to a config setting
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	ctx := context.Background()
	_, err = cpcRw.Conn.ExecContext(ctx, `
		INSERT OR REPLACE INTO config (key, value, help_text, created_at, updated_at)
		VALUES (?, ?, ?, UNIXEPOCH('now'), UNIXEPOCH('now'))
	`, "listener_address", "0.0.0.0", "IP address or hostname to bind to (e.g., 0.0.0.0 for all interfaces)")
	if err != nil {
		t.Fatalf("Failed to insert help text: %v", err)
	}

	// Reload config
	if loadErr := app.loadConfig(); loadErr != nil {
		t.Fatalf("Failed to reload config: %v", loadErr)
	}

	// Make authenticated request
	req.Header.Set("Cookie", w.Header().Get("Set-Cookie"))
	w2 := httptest.NewRecorder()
	app.configHandlers.ConfigGet(w2, req)

	if w2.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w2.Code)
	}

	// Parse HTML response
	doc, err := html.Parse(strings.NewReader(w2.Body.String()))
	if err != nil {
		t.Fatalf("Failed to parse HTML: %v", err)
	}

	// Look for tooltip element (DaisyUI tooltip has data-tip attribute)
	tooltipFound := false
	var searchTooltip func(*html.Node)
	searchTooltip = func(n *html.Node) {
		if n.Type == html.ElementNode {
			// Check for DaisyUI tooltip attributes
			var hasTooltipClass bool
			var dataTipValue string
			for _, attr := range n.Attr {
				if attr.Key == "data-tip" {
					dataTipValue = attr.Val
				}
				if attr.Key == "class" && strings.Contains(attr.Val, "tooltip") {
					hasTooltipClass = true
				}
			}
			// Check if data-tip attribute contains our help text
			if dataTipValue != "" && strings.Contains(dataTipValue, "IP address or hostname") {
				tooltipFound = true
				return
			}
			// Also check text content if it's a tooltip element
			if hasTooltipClass {
				var textContent strings.Builder
				var extractText func(*html.Node)
				extractText = func(node *html.Node) {
					if node.Type == html.TextNode {
						textContent.WriteString(node.Data)
					}
					for c := node.FirstChild; c != nil; c = c.NextSibling {
						extractText(c)
					}
				}
				extractText(n)
				if strings.Contains(textContent.String(), "IP address or hostname") {
					tooltipFound = true
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if !tooltipFound {
				searchTooltip(c)
			}
		}
	}
	searchTooltip(doc)

	if !tooltipFound {
		t.Error("Tooltip with help text not found in HTML response")
	}
}

// TestConfigHelp_ExamplesShown verifies that example values are displayed correctly
// in the configuration UI for fields that have example_value set in the database.
func TestConfigHelp_ExamplesShown(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	// Set up authenticated session
	req := httptest.NewRequest("GET", "/config", nil)
	w := httptest.NewRecorder()

	session, err := app.store.Get(req, "session-name")
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}
	session.Values["authenticated"] = true
	if saveErr := session.Save(req, w); saveErr != nil {
		t.Fatalf("failed to save session: %v", saveErr)
	}

	// Add example value to a duration setting
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	ctx := context.Background()
	_, err = cpcRw.Conn.ExecContext(ctx, `
		INSERT OR REPLACE INTO config (key, value, help_text, example_value, created_at, updated_at)
		VALUES (?, ?, ?, ?, UNIXEPOCH('now'), UNIXEPOCH('now'))
	`, "cache_max_time", "720h", "Maximum time entries remain in cache", "30m, 2h, 7d, 720h")
	if err != nil {
		t.Fatalf("Failed to insert example value: %v", err)
	}

	// Reload config
	if loadErr := app.loadConfig(); loadErr != nil {
		t.Fatalf("Failed to reload config: %v", loadErr)
	}

	// Make authenticated request - request Performance tab where cache_max_time is located
	req.Header.Set("Cookie", w.Header().Get("Set-Cookie"))
	// Update URL to request Performance tab (cache_max_time is in Performance tab, not default Server tab)
	req = httptest.NewRequest("GET", "/config?category=performance", nil)
	req.Header.Set("Cookie", w.Header().Get("Set-Cookie"))
	w2 := httptest.NewRecorder()
	app.configHandlers.ConfigGet(w2, req)

	if w2.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w2.Code)
	}

	// Parse HTML response
	doc, err := html.Parse(strings.NewReader(w2.Body.String()))
	if err != nil {
		t.Fatalf("Failed to parse HTML: %v", err)
	}

	// Look for example value in the response
	exampleFound := false
	var searchExample func(*html.Node)
	searchExample = func(n *html.Node) {
		if n.Type == html.TextNode {
			// Check for example format patterns
			if strings.Contains(n.Data, "30m") ||
				strings.Contains(n.Data, "2h") ||
				strings.Contains(n.Data, "7d") ||
				strings.Contains(n.Data, "720h") {
				exampleFound = true
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if !exampleFound {
				searchExample(c)
			}
		}
	}
	searchExample(doc)

	if !exampleFound {
		t.Error("Example values (30m, 2h, 7d, 720h) not found in HTML response")
	}
}
