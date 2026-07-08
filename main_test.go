package main

import (
	"bytes"
	"errors"
	"os"
	"syscall"
	"testing"

	"github.com/lbe/sfpg-go/internal/getopt"
)

// fakeApp is a lightweight implementation of appServer for unit tests.
type fakeApp struct {
	initUnlockErr    error
	initUnlockCalled bool
	unlockErr        error
	unlockCalled     bool
	unlockUsername   string

	initETagErr      error
	initETagCalled   bool
	incrementETagErr error
	etagValue        string

	initBatchErr    error
	initBatchCalled bool
	batchCode       int
	batchLoadCalled bool

	runErr     error
	runCalled  bool
	runStarted chan struct{}
	runBlock   chan struct{}

	logProfileCalled bool
	shutdownCalled   bool
}

func newFakeApp() *fakeApp {
	return &fakeApp{runStarted: make(chan struct{})}
}

func (f *fakeApp) InitForUnlock() error {
	f.initUnlockCalled = true
	return f.initUnlockErr
}

func (f *fakeApp) UnlockAccount(username string) error {
	f.unlockCalled = true
	f.unlockUsername = username
	return f.unlockErr
}

func (f *fakeApp) InitForIncrementETag(opt getopt.Opt) error {
	f.initETagCalled = true
	return f.initETagErr
}

func (f *fakeApp) IncrementETag() (string, error) {
	return f.etagValue, f.incrementETagErr
}

func (f *fakeApp) InitForBatchLoad(opt getopt.Opt) error {
	f.initBatchCalled = true
	return f.initBatchErr
}

func (f *fakeApp) RunCacheBatchLoad() int {
	f.batchLoadCalled = true
	return f.batchCode
}

func (f *fakeApp) Run(_, _ int) error {
	f.runCalled = true
	close(f.runStarted)
	if f.runBlock != nil {
		<-f.runBlock
	}
	return f.runErr
}

func (f *fakeApp) LogProfileLocation() { f.logProfileCalled = true }
func (f *fakeApp) Shutdown()           { f.shutdownCalled = true }

// setupMainHooks saves and restores the package-level hooks used by runMain.
func setupMainHooks(t *testing.T) {
	t.Helper()
	origParse := parseOptions
	origNewApp := newApp
	origNotify := notifySignals
	origStdout := stdout
	origExit := osExit

	t.Cleanup(func() {
		parseOptions = origParse
		newApp = origNewApp
		notifySignals = origNotify
		stdout = origStdout
		osExit = origExit
	})
}

func TestRunMain_UnlockAccount(t *testing.T) {
	setupMainHooks(t)

	tests := []struct {
		name     string
		opt      getopt.Opt
		fake     *fakeApp
		wantCode int
	}{
		{
			name: "success",
			opt:  getopt.Opt{UnlockAccount: getopt.OptString{String: "admin", IsSet: true}},
			fake: func() *fakeApp {
				f := newFakeApp()
				return f
			}(),
			wantCode: 0,
		},
		{
			name: "init_error",
			opt:  getopt.Opt{UnlockAccount: getopt.OptString{String: "admin", IsSet: true}},
			fake: func() *fakeApp {
				f := newFakeApp()
				f.initUnlockErr = errors.New("init failed")
				return f
			}(),
			wantCode: 1,
		},
		{
			name: "unlock_error",
			opt:  getopt.Opt{UnlockAccount: getopt.OptString{String: "admin", IsSet: true}},
			fake: func() *fakeApp {
				f := newFakeApp()
				f.unlockErr = errors.New("unlock failed")
				return f
			}(),
			wantCode: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parseOptions = func() getopt.Opt { return tt.opt }
			newApp = func(getopt.Opt, string) appServer { return tt.fake }

			code := runMain()
			if code != tt.wantCode {
				t.Errorf("runMain() = %d, want %d", code, tt.wantCode)
			}
			if !tt.fake.initUnlockCalled {
				t.Errorf("InitForUnlock was not called")
			}
			if tt.name == "success" {
				if !tt.fake.unlockCalled {
					t.Errorf("UnlockAccount was not called")
				}
				if tt.fake.unlockUsername != "admin" {
					t.Errorf("UnlockAccount username = %q, want %q", tt.fake.unlockUsername, "admin")
				}
			}
		})
	}
}

func TestRunMain_IncrementETag(t *testing.T) {
	setupMainHooks(t)

	tests := []struct {
		name        string
		opt         getopt.Opt
		fake        *fakeApp
		wantCode    int
		wantStdout  string
		wantETagVal string
	}{
		{
			name:        "success",
			opt:         getopt.Opt{IncrementETag: getopt.OptBool{Bool: true, IsSet: true}},
			fake:        func() *fakeApp { f := newFakeApp(); f.etagValue = "etag-2"; return f }(),
			wantCode:    0,
			wantStdout:  "ETag version incremented to: etag-2\n",
			wantETagVal: "etag-2",
		},
		{
			name:     "init_error",
			opt:      getopt.Opt{IncrementETag: getopt.OptBool{Bool: true, IsSet: true}},
			fake:     func() *fakeApp { f := newFakeApp(); f.initETagErr = errors.New("init failed"); return f }(),
			wantCode: 1,
		},
		{
			name:     "increment_error",
			opt:      getopt.Opt{IncrementETag: getopt.OptBool{Bool: true, IsSet: true}},
			fake:     func() *fakeApp { f := newFakeApp(); f.incrementETagErr = errors.New("increment failed"); return f }(),
			wantCode: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			stdout = &buf
			parseOptions = func() getopt.Opt { return tt.opt }
			newApp = func(getopt.Opt, string) appServer { return tt.fake }

			code := runMain()
			if code != tt.wantCode {
				t.Errorf("runMain() = %d, want %d", code, tt.wantCode)
			}
			if tt.wantStdout != "" && buf.String() != tt.wantStdout {
				t.Errorf("stdout = %q, want %q", buf.String(), tt.wantStdout)
			}
		})
	}
}

func TestRunMain_CacheBatchLoad(t *testing.T) {
	setupMainHooks(t)

	tests := []struct {
		name     string
		fake     *fakeApp
		wantCode int
	}{
		{
			name:     "success",
			fake:     func() *fakeApp { f := newFakeApp(); f.batchCode = 0; return f }(),
			wantCode: 0,
		},
		{
			name:     "init_error",
			fake:     func() *fakeApp { f := newFakeApp(); f.initBatchErr = errors.New("init failed"); return f }(),
			wantCode: 1,
		},
		{
			name:     "run_returns_error",
			fake:     func() *fakeApp { f := newFakeApp(); f.batchCode = 1; return f }(),
			wantCode: 1,
		},
	}

	opt := getopt.Opt{CacheBatchLoad: getopt.OptBool{Bool: true, IsSet: true}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parseOptions = func() getopt.Opt { return opt }
			newApp = func(getopt.Opt, string) appServer { return tt.fake }

			code := runMain()
			if code != tt.wantCode {
				t.Errorf("runMain() = %d, want %d", code, tt.wantCode)
			}
			if tt.name != "init_error" && !tt.fake.shutdownCalled {
				t.Errorf("Shutdown was not called")
			}
		})
	}
}

func TestRunMain_RunServer_ReturnsNil(t *testing.T) {
	setupMainHooks(t)

	fake := newFakeApp()
	parseOptions = func() getopt.Opt { return getopt.Opt{} }
	newApp = func(getopt.Opt, string) appServer { return fake }
	notifySignals = func(chan<- os.Signal, ...os.Signal) {}

	code := runMain()
	if code != 0 {
		t.Errorf("runMain() = %d, want 0", code)
	}
	if !fake.logProfileCalled {
		t.Errorf("LogProfileLocation was not called")
	}
	if !fake.shutdownCalled {
		t.Errorf("Shutdown was not called")
	}
}

func TestRunMain_RunServer_ReturnsError(t *testing.T) {
	setupMainHooks(t)

	fake := newFakeApp()
	fake.runErr = errors.New("run failed")
	parseOptions = func() getopt.Opt { return getopt.Opt{} }
	newApp = func(getopt.Opt, string) appServer { return fake }
	notifySignals = func(chan<- os.Signal, ...os.Signal) {}

	code := runMain()
	if code != 1 {
		t.Errorf("runMain() = %d, want 1", code)
	}
	if !fake.logProfileCalled {
		t.Errorf("LogProfileLocation was not called")
	}
	if !fake.shutdownCalled {
		t.Errorf("Shutdown was not called")
	}
}

func TestRunMain_RunServer_SignalShutdown(t *testing.T) {
	setupMainHooks(t)

	fake := newFakeApp()
	fake.runBlock = make(chan struct{})
	t.Cleanup(func() { close(fake.runBlock) })

	var sigChan chan<- os.Signal
	registered := make(chan struct{})
	notifySignals = func(c chan<- os.Signal, _ ...os.Signal) {
		sigChan = c
		close(registered)
	}

	parseOptions = func() getopt.Opt { return getopt.Opt{} }
	newApp = func(getopt.Opt, string) appServer { return fake }

	done := make(chan int)
	go func() { done <- runMain() }()

	<-registered
	<-fake.runStarted
	sigChan <- syscall.SIGINT

	code := <-done
	if code != 0 {
		t.Errorf("runMain() = %d, want 0", code)
	}
	if !fake.logProfileCalled {
		t.Errorf("LogProfileLocation was not called")
	}
	if !fake.shutdownCalled {
		t.Errorf("Shutdown was not called")
	}
}

func TestRunMain_PanicRecovery(t *testing.T) {
	setupMainHooks(t)

	parseOptions = func() getopt.Opt { return getopt.Opt{} }
	newApp = func(getopt.Opt, string) appServer { panic("boom") }

	code := runMain()
	if code != 1 {
		t.Errorf("runMain() = %d, want 1", code)
	}
}

func TestMain_ExitCodePassthrough(t *testing.T) {
	setupMainHooks(t)

	notifySignals = func(chan<- os.Signal, ...os.Signal) {}
	parseOptions = func() getopt.Opt { return getopt.Opt{} }

	tests := []struct {
		name     string
		runErr   error
		wantCode int
	}{
		{name: "success", runErr: nil, wantCode: 0},
		{name: "error", runErr: errors.New("run failed"), wantCode: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeApp()
			fake.runErr = tt.runErr
			newApp = func(getopt.Opt, string) appServer { return fake }

			var captured int
			osExit = func(code int) { captured = code }

			main()

			if captured != tt.wantCode {
				t.Errorf("main() exit code = %d, want %d", captured, tt.wantCode)
			}
		})
	}
}
