package cachelite

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCaptureBodyWriter_Commit verifies that CaptureBodyWriter + CommitCapturedBody
// writes the body to the underlying ResponseWriter with status 200.
func TestCaptureBodyWriter_Commit(t *testing.T) {
	body := []byte("<html><body>test page</body></html>")
	recorder := httptest.NewRecorder()
	ccw := &cacheCapturingWriter{
		ResponseWriter: recorder,
		body:           make([]byte, 0, 65536),
	}

	sink := ccw.CaptureBodyWriter()
	n, err := sink.Write(body)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(body) {
		t.Errorf("sink.Write returned n=%d, want %d", n, len(body))
	}

	// ccw.body must contain the captured data
	if string(ccw.body) != string(body) {
		t.Errorf("ccw.body = %q, want %q", string(ccw.body), string(body))
	}

	// Recorder must be untouched before commit
	if recorder.Body.Len() != 0 {
		t.Errorf("recorder body before commit = %d bytes, want 0", recorder.Body.Len())
	}

	// Commit should flush to recorder
	if err := ccw.CommitCapturedBody(); err != nil {
		t.Fatal(err)
	}
	if recorder.Body.String() != string(body) {
		t.Errorf("recorder body after commit = %q, want %q", recorder.Body.String(), string(body))
	}
	if recorder.Code != http.StatusOK {
		t.Errorf("recorder code = %d, want %d", recorder.Code, http.StatusOK)
	}
}

// TestCaptureBodyWriter_NoCommit verifies that sink writes alone do NOT commit
// the body or a status code to the underlying ResponseWriter. The internal
// wroteHeader and statusCode fields are checked directly because Go's
// httptest.ResponseRecorder may default Code to 200.
func TestCaptureBodyWriter_NoCommit(t *testing.T) {
	body := []byte("<html><body>test page</body></html>")
	recorder := httptest.NewRecorder()
	ccw := &cacheCapturingWriter{
		ResponseWriter: recorder,
		body:           make([]byte, 0, 65536),
	}

	sink := ccw.CaptureBodyWriter()
	sink.Write(body)

	// Recorder must be completely untouched: no body written through.
	if recorder.Body.Len() != 0 {
		t.Errorf("recorder body = %d bytes, want 0 (sink must not write through)", recorder.Body.Len())
	}

	// Internal flags must show no header was committed.
	if ccw.wroteHeader {
		t.Error("ccw.wroteHeader is true, want false (sink must not commit status)")
	}
	if ccw.statusCode != 0 {
		t.Errorf("ccw.statusCode = %d, want 0 (sink must not set statusCode)", ccw.statusCode)
	}

	// ccw.body must still have the captured data
	if string(ccw.body) != string(body) {
		t.Errorf("ccw.body = %q, want %q", string(ccw.body), string(body))
	}
}

// TestCaptureBodyWriter_ResetThen500 verifies the render-failure regression guard:
// sink writes → ResetCapturedBody → WriteHeader(500) + Write(errBody) → recorder
// gets status 500 and only errBody.
func TestCaptureBodyWriter_ResetThen500(t *testing.T) {
	body := []byte("<html><body>rendered page</body></html>")
	errBody := []byte("error page\n")
	recorder := httptest.NewRecorder()
	ccw := &cacheCapturingWriter{
		ResponseWriter: recorder,
		body:           make([]byte, 0, 65536),
	}

	// Simulate partial render into sink
	sink := ccw.CaptureBodyWriter()
	sink.Write(body)

	// Render failure: reset captured body
	ccw.ResetCapturedBody()

	// Handler error path: write 500 directly
	ccw.WriteHeader(http.StatusInternalServerError)
	ccw.Write(errBody)

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("recorder code = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	if recorder.Body.String() != string(errBody) {
		t.Errorf("recorder body = %q, want %q (must not contain partial render)", recorder.Body.String(), string(errBody))
	}
}

// TestResetCapturedBody_LengthAndCapacity verifies that ResetCapturedBody clears
// the body length while retaining the backing array capacity, and leaves
// wroteHeader untouched.
func TestResetCapturedBody_LengthAndCapacity(t *testing.T) {
	body := []byte("<html><body>test</body></html>")
	ccw := &cacheCapturingWriter{
		ResponseWriter: httptest.NewRecorder(),
		body:           make([]byte, 0, 65536),
	}
	ccw.body = append(ccw.body, body...)

	capBefore := cap(ccw.body)
	wroteHeaderBefore := ccw.wroteHeader

	ccw.ResetCapturedBody()

	if len(ccw.body) != 0 {
		t.Errorf("len after reset = %d, want 0", len(ccw.body))
	}
	if cap(ccw.body) != capBefore {
		t.Errorf("cap after reset = %d, want %d (cap must be retained)", cap(ccw.body), capBefore)
	}
	if ccw.wroteHeader != wroteHeaderBefore {
		t.Errorf("wroteHeader changed from %v to %v (must be untouched)", wroteHeaderBefore, ccw.wroteHeader)
	}
}

// TestCacheCapturingWriter_WriteRegression verifies that the existing Write
// method still appends to ccw.body AND writes through to the client.
func TestCacheCapturingWriter_WriteRegression(t *testing.T) {
	body := []byte("<html><body>regression test</body></html>")
	recorder := httptest.NewRecorder()
	ccw := &cacheCapturingWriter{
		ResponseWriter: recorder,
		body:           make([]byte, 0, 65536),
	}

	n, err := ccw.Write(body)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(body) {
		t.Errorf("Write returned n=%d, want %d", n, len(body))
	}

	// ccw.body must contain the data (append behavior preserved)
	if string(ccw.body) != string(body) {
		t.Errorf("ccw.body = %q, want %q", string(ccw.body), string(body))
	}

	// Recorder must have the data (write-through preserved)
	if recorder.Body.String() != string(body) {
		t.Errorf("recorder body = %q, want %q", recorder.Body.String(), string(body))
	}

	// Status must be 200 (default via Write)
	if recorder.Code != http.StatusOK {
		t.Errorf("recorder code = %d, want %d", recorder.Code, http.StatusOK)
	}
}
