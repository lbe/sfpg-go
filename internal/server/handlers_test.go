package server

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerylib"
)

// ============================================================================
// Main Handler Tests
// ============================================================================

func TestRootHandler(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	server := httptest.NewServer(app.getRouter())
	defer server.Close()

	// Use a client that does not follow redirects
	client := &http.Client{
		CheckRedirect: func( /*req*/ _ *http.Request /*via*/, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	t.Run("Unauthenticated access to root", func(t *testing.T) {
		resp, err := client.Get(server.URL + "/")
		if err != nil {
			t.Fatalf("client.Get failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusFound {
			t.Errorf("Expected status code %d, got %d", http.StatusFound, resp.StatusCode)
		}

		location, err := resp.Location()
		if err != nil {
			t.Fatalf("Failed to get redirect location: %v", err)
		}

		if location.Path != "/gallery/1" {
			t.Errorf("Expected redirect to /gallery/1, got %s", location.Path)
		}
	})

	t.Run("Authenticated access to root", func(t *testing.T) {
		req, err := http.NewRequest("GET", server.URL+"/", nil)
		if err != nil {
			t.Fatalf("http.NewRequest: %v", err)
		}
		req.AddCookie(MakeAuthCookie(t, app))

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("client.Do failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusFound {
			t.Errorf("Expected status code %d, got %d", http.StatusFound, resp.StatusCode)
		}

		location, err := resp.Location()
		if err != nil {
			t.Fatalf("Failed to get redirect location: %v", err)
		}

		if location.Path != "/gallery/1" {
			t.Errorf("Expected redirect to /gallery/1*, got %s", location.String())
		}
	})

	t.Run("Unrecognized path", func(t *testing.T) {
		req, err := http.NewRequest("GET", server.URL+"/unrecognized/path", nil)
		if err != nil {
			t.Fatalf("http.NewRequest: %v", err)
		}
		req.AddCookie(MakeAuthCookie(t, app))

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("client.Do failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("Expected status code %d, got %d", http.StatusBadRequest, resp.StatusCode)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read response body: %v", err)
		}
		expectedBody := "400 Bad Request\n"
		if string(body) != expectedBody {
			t.Errorf("Expected body %q, got %q", expectedBody, string(body))
		}
	})
}

func TestHXPushURLRegression(t *testing.T) {
	app := CreateApp(t, WithPool())
	time.Sleep(200 * time.Millisecond)
	defer app.Shutdown()

	router := app.getRouter()
	server := httptest.NewServer(router)
	defer server.Close()

	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	// Create test folder and image
	importer := gallerylib.Importer{Q: cpcRw.Queries}
	file, err := importer.UpsertPathChain(app.ctx, "/test-gallery/test-image.jpg", 100, 0, "", 0, 0, 0, "image/jpeg")
	if err != nil {
		t.Fatalf("failed to import test file: %v", err)
	}
	folderID := file.FolderID.Int64
	fileID := file.ID

	// Get current version
	version := app.GetETagVersion()

	authCookie := MakeAuthCookie(t, app)

	tests := []struct {
		name     string
		path     string
		expected string // empty means handler must NOT set HX-Push-URL (lightbox/info so back/j works)
	}{
		{
			name:     "Gallery",
			path:     fmt.Sprintf("/gallery/%d", folderID),
			expected: fmt.Sprintf("/gallery/%d?v=%s", folderID, version),
		},
		{
			name:     "Image",
			path:     fmt.Sprintf("/image/%d", fileID),
			expected: fmt.Sprintf("/image/%d?v=%s", fileID, version),
		},
		{
			name:     "Lightbox",
			path:     fmt.Sprintf("/lightbox/%d", fileID),
			expected: "", // must not push URL so back/j after close goes to previous folder
		},
		{
			name:     "Info Folder",
			path:     fmt.Sprintf("/info/folder/%d", folderID),
			expected: "", // must not push URL (info is partial; lightbox loads /info/image and must not change URL)
		},
		{
			name:     "Info Image",
			path:     fmt.Sprintf("/info/image/%d", fileID),
			expected: "", // must not push URL so opening lightbox does not change URL; back/j works after close
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", server.URL+tt.path, nil)
			if err != nil {
				t.Fatalf("http.NewRequest: %v", err)
			}
			req.AddCookie(authCookie)
			// Header should be present even for non-HTMX requests (safe)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("%s: request failed: %v", tt.name, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("%s: expected status 200, got %d", tt.name, resp.StatusCode)
			}

			pushURL := resp.Header.Get("HX-Push-URL")
			if pushURL != tt.expected {
				t.Errorf("%s: expected HX-Push-URL %q, got %q", tt.name, tt.expected, pushURL)
			}
		})
	}
}

// ============================================================================
// Config Export/Import Handler Tests
// ============================================================================

// ============================================================================
// Config Restore/Restart Handler Tests
// ============================================================================
