package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/lbe/sfpg-go/internal/testutil"
)

func assertConfigValidationGlobal(t *testing.T, w *httptest.ResponseRecorder, wantMessage string) {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%q", w.Code, http.StatusOK, w.Body.String())
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	errMsg := testutil.FindElementByID(doc, "config-error-message")
	if errMsg == nil {
		t.Fatal("missing #config-error-message")
	}
	entry := testutil.FindElement(errMsg, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "div" &&
			testutil.GetAttr(n, "data-error-key") == "_global"
	})
	if entry == nil {
		t.Fatal("missing _global validation error entry")
	}
	if got := testutil.GetAttr(entry, "data-error-message"); got != wantMessage {
		t.Errorf("data-error-message = %q, want %q", got, wantMessage)
	}
}

func assertConfigMalformedRequest(t *testing.T, w *httptest.ResponseRecorder, wantMessage string) {
	t.Helper()
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if got := w.Header().Get("HX-Retarget"); got != "#config-error-message" {
		t.Errorf("HX-Retarget = %q, want #config-error-message", got)
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	errMsg := testutil.FindElementByID(doc, "config-error-message")
	if errMsg == nil {
		t.Fatal("missing #config-error-message")
	}
	if got := strings.TrimSpace(testutil.GetTextContent(errMsg)); got != wantMessage {
		t.Errorf("error message = %q, want %q", got, wantMessage)
	}
}
