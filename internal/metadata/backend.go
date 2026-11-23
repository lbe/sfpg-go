package metadata

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	wasmexif "github.com/lbe/go-exiftool-wasm"
	nativeexif "github.com/ncruces/go-exiftool"
)

// server is one ExifTool process, WASM or native.
type server interface {
	Command(arg ...string) ([]byte, error)
	Shutdown() error
}

var (
	_ server = (*wasmexif.Server)(nil)
	_ server = nativeServer{}
)

// nativeServer is a native ExifTool server. Its Shutdown ignores the exit
// status: ExifTool exits with status 1 when its last command failed, and
// Extract has already returned that failure. Other errors are still returned.
type nativeServer struct {
	*nativeexif.Server
}

// Shutdown stops the server and returns any error other than an exit status.
func (s nativeServer) Shutdown() error {
	err := s.Server.Shutdown()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil
	}
	return err
}

// minNativeVersion is the oldest native ExifTool that is used.
const minNativeVersion = 13.00

// backendChoice is the ExifTool backend an Extractor runs.
type backendChoice struct {
	native  bool
	path    string
	version string
}

// chooseBackend uses the native exiftool on PATH when it is recent enough,
// and the WASM backend otherwise, and logs the choice.
func chooseBackend() backendChoice {
	path, version, ok := detectNative()
	if !ok {
		slog.Info("metadata backend", "backend", "wasm", "exiftool", path, "version", version)
		if path != "" {
			slog.Info("metadata: native exiftool not used", "path", path, "version", version, "min", minNativeVersion)
		}
		return backendChoice{}
	}
	nativeexif.Exec = path
	slog.Info("metadata backend", "backend", "native", "exiftool", path, "version", version)
	return backendChoice{native: true, path: path, version: version}
}

// detectNative finds the exiftool on PATH and reads its version. path is empty
// when there is none. version is empty when the version cannot be read. ok is
// set when the version is at least minNativeVersion.
func detectNative() (path string, version string, ok bool) {
	path, err := exec.LookPath("exiftool")
	if err != nil {
		return "", "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "-ver").Output()
	if err != nil {
		return path, "", false
	}
	version = strings.TrimSpace(string(out))
	v, err := strconv.ParseFloat(version, 64)
	if err != nil {
		return path, version, false
	}
	return path, version, v >= minNativeVersion
}

// wasmPoolSize returns the size of the WASM pool.
func wasmPoolSize() int { return min(4, max(2, runtime.NumCPU()/3)) }

// newServer starts an ExifTool server of the backend b.
//
//nolint:ireturn // R8: the server interface has two production implementations.
func newServer(b backendChoice) (server, error) {
	if b.native {
		s, err := nativeexif.NewServer()
		if err != nil {
			return nil, err
		}
		return nativeServer{s}, nil
	}
	s, err := wasmexif.NewServer()
	if err != nil {
		return nil, err
	}
	return s, nil
}
