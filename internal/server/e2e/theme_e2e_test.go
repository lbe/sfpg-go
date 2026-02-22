//go:build e2e

package e2e

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
)

// TestThemeChangeBrowserE2E tests the full theme change flow in a real browser.
// This test requires:
//   - Playwright MCP server to be installed and configured
//   - The application to be running on localhost:8083 (or configure baseURL)
//
// Run with: go test -tags=e2e ./internal/server/e2e/... -v
func TestThemeChangeBrowserE2E(t *testing.T) {
	baseURL := "http://localhost:8083" // Adjust if your dev server runs elsewhere
	setupPlaywright(t)

	// Step 1: Navigate to gallery page
	t.Run("navigate_to_gallery", func(t *testing.T) {
		status, err := playwrightNavigate(baseURL + "/gallery/1")
		if err != nil {
			t.Fatalf("failed to navigate: %v", err)
		}
		if status < http.StatusOK || status >= http.StatusBadRequest {
			t.Fatalf("navigation failed with status: %d", status)
		}
		t.Log("Navigated to gallery page")
	})

	// Step 2: Verify initial theme is dark
	t.Run("verify_initial_dark_theme", func(t *testing.T) {
		theme, err := playwrightGetAttribute("html", "data-theme")
		if err != nil {
			t.Fatalf("failed to get theme: %v", err)
		}
		if theme != "dark" {
			t.Errorf("initial theme = %q, want dark", theme)
		}
		t.Logf("Initial theme: %s", theme)
	})

	// Step 3: Click theme selector button to open modal
	t.Run("open_theme_modal", func(t *testing.T) {
		// Open the hamburger menu, then click the Theme link.
		err := playwrightClick("#hamburger-menu-btn")
		if err != nil {
			t.Fatalf("failed to click hamburger menu: %v", err)
		}
		if err := playwrightClick("#hamburger-menu-items a[aria-label='Theme']"); err != nil {
			t.Fatalf("failed to click theme menu item: %v", err)
		}
		if err := playwrightWaitForSelectorAttached("#theme_modal"); err != nil {
			t.Fatalf("theme modal did not attach: %v", err)
		}
		if err := playwrightWaitForChecked("#theme_modal"); err != nil {
			t.Fatalf("theme modal did not open: %v", err)
		}
		time.Sleep(500 * time.Millisecond) // Wait for modal animation
		t.Log("Opened theme modal")
	})

	// Step 4: Verify modal is open
	t.Run("verify_modal_open", func(t *testing.T) {
		checked, err := playwrightIsChecked("#theme_modal")
		if err != nil {
			t.Fatalf("failed to read modal state: %v", err)
		}
		if !checked {
			t.Fatalf("theme modal not open")
		}
		t.Log("Theme modal is visible")
	})

	// Step 5: Click on light theme card
	t.Run("select_light_theme", func(t *testing.T) {
		// Find and click the light theme card
		err := playwrightClick("[data-theme='light'].theme-card")
		if err != nil {
			t.Fatalf("failed to click light theme card: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
		t.Log("Selected light theme")
	})

	// Step 6: Verify hidden input was updated
	t.Run("verify_hidden_input_updated", func(t *testing.T) {
		value, err := playwrightGetInputValue("#theme-selected-value")
		if err != nil {
			t.Fatalf("failed to get hidden input value: %v", err)
		}
		if value != "light" {
			t.Errorf("hidden input value = %q, want light", value)
		}
		t.Logf("Hidden input value: %s", value)
	})

	// Step 7: Click Apply button
	t.Run("click_apply_button", func(t *testing.T) {
		err := playwrightClick("#theme-apply-btn")
		if err != nil {
			t.Fatalf("failed to click apply button: %v", err)
		}
		t.Log("Clicked apply button")
	})

	// Step 8: Wait for page reload and verify new theme
	t.Run("verify_theme_changed_after_reload", func(t *testing.T) {
		// Wait for reload
		time.Sleep(2 * time.Second)

		// Re-navigate to ensure we're on the page
		_, err := playwrightNavigate(baseURL + "/gallery/1")
		if err != nil {
			t.Fatalf("failed to navigate after theme change: %v", err)
		}

		// Check theme attribute
		theme, err := playwrightGetAttribute("html", "data-theme")
		if err != nil {
			t.Fatalf("failed to get theme after change: %v", err)
		}
		if theme != "light" {
			t.Errorf("theme after change = %q, want light", theme)
		}
		t.Logf("Theme after reload: %s", theme)
	})

	// Step 9: Verify cookie was set
	t.Run("verify_theme_cookie", func(t *testing.T) {
		cookies, err := playwrightGetCookies(baseURL)
		if err != nil {
			t.Fatalf("failed to get cookies: %v", err)
		}

		found := false
		for _, cookie := range cookies {
			if cookie.Name == "theme" && cookie.Value == "light" {
				found = true
				break
			}
		}

		if !found {
			t.Errorf("theme cookie not set to light")
			return
		}
		t.Log("Verified theme cookie is light")
	})
}

// playwrightNavigate navigates to a URL using Playwright MCP
func playwrightNavigate(url string) (int, error) {
	if page == nil {
		return 0, errors.New("playwright not initialized")
	}
	resp, err := page.Goto(url, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateNetworkidle,
	})
	if err != nil {
		return 0, err
	}
	if resp == nil {
		return 0, fmt.Errorf("no response for navigation to %s", url)
	}
	return resp.Status(), nil
}

// playwrightClick clicks an element using Playwright MCP
func playwrightClick(selector string) error {
	if page == nil {
		return errors.New("playwright not initialized")
	}
	return page.Click(selector)
}

// playwrightGetAttribute gets an attribute value using Playwright MCP
func playwrightGetAttribute(selector, attribute string) (string, error) {
	if page == nil {
		return "", errors.New("playwright not initialized")
	}
	return page.GetAttribute(selector, attribute)
}

// playwrightGetInputValue gets input value using Playwright MCP
func playwrightGetInputValue(selector string) (string, error) {
	if page == nil {
		return "", errors.New("playwright not initialized")
	}
	return page.InputValue(selector)
}

// playwrightIsVisible checks if element is visible
func playwrightIsVisible(selector string) (bool, error) {
	if page == nil {
		return false, errors.New("playwright not initialized")
	}
	return page.IsVisible(selector)
}

func playwrightIsChecked(selector string) (bool, error) {
	if page == nil {
		return false, errors.New("playwright not initialized")
	}
	return page.IsChecked(selector)
}

func playwrightWaitForSelectorAttached(selector string) error {
	if page == nil {
		return errors.New("playwright not initialized")
	}
	_, err := page.WaitForSelector(selector, playwright.PageWaitForSelectorOptions{
		State: playwright.WaitForSelectorStateAttached,
	})
	return err
}

func playwrightWaitForChecked(selector string) error {
	if page == nil {
		return errors.New("playwright not initialized")
	}
	_, err := page.WaitForFunction(
		`(selector) => {
  const el = document.querySelector(selector);
  return !!el && el.checked === true;
}`,
		selector,
	)
	return err
}

// Cookie represents a browser cookie
type Cookie struct {
	Name  string
	Value string
}

// playwrightGetCookies gets browser cookies
func playwrightGetCookies(url string) ([]Cookie, error) {
	if browserContext == nil {
		return nil, errors.New("playwright not initialized")
	}
	ctxCookies, err := browserContext.Cookies(url)
	if err != nil {
		return nil, err
	}
	cookies := make([]Cookie, 0, len(ctxCookies))
	for _, cookie := range ctxCookies {
		cookies = append(cookies, Cookie{Name: cookie.Name, Value: cookie.Value})
	}
	return cookies, nil
}

var (
	pw             *playwright.Playwright
	browser        playwright.Browser
	browserContext playwright.BrowserContext
	page           playwright.Page
)

func setupPlaywright(t *testing.T) {
	t.Helper()
	if pw != nil {
		return
	}

	var err error
	pw, err = playwright.Run()
	if err != nil {
		t.Fatalf("failed to start Playwright: %v", err)
	}

	browser, err = pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
	})
	if err != nil {
		t.Fatalf("failed to launch browser: %v", err)
	}

	browserContext, err = browser.NewContext()
	if err != nil {
		t.Fatalf("failed to create browser context: %v", err)
	}

	page, err = browserContext.NewPage()
	if err != nil {
		t.Fatalf("failed to create page: %v", err)
	}

	t.Cleanup(func() {
		if page != nil {
			_ = page.Close()
			page = nil
		}
		if browserContext != nil {
			_ = browserContext.Close()
			browserContext = nil
		}
		if browser != nil {
			_ = browser.Close()
			browser = nil
		}
		if pw != nil {
			_ = pw.Stop()
			pw = nil
		}
	})
}
