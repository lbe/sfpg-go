package server

// Thumb is a minimal test struct used across server test files.
type Thumb struct {
	ID        int64
	Path      string
	ThumbPath string
	DispName  string
	IsImage   bool
}
