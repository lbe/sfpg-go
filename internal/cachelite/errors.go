package cachelite

import "github.com/lbe/sfpg-go/internal/cachelite/bodycodec"

// ErrUnrecognizedCacheBody is returned when a cached body cannot be decoded.
// The middleware treats this as a cache MISS (not a 500).
var ErrUnrecognizedCacheBody = bodycodec.ErrUnrecognizedCacheBody
