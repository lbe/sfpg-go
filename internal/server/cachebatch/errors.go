package cachebatch

import "errors"

var (
	// ErrDiscoveryActive is returned when batch load is blocked because discovery is running.
	ErrDiscoveryActive = errors.New("cache batch load blocked: discovery is active")
	// ErrAlreadyRunning is returned when a batch load run is already in progress.
	ErrAlreadyRunning = errors.New("cache batch load already running")
)

var errNilHandler = errors.New("handler is nil")
