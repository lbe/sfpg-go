//go:build linux || darwin

package evalsymlinks

import "os"

// unixCleanInPlace applies Unix filepath.Clean semantics to dest[:n], writing the
// result to dest[:m] and returning m.
func unixCleanInPlace(dest []byte, n int) (int, error) {
	if n == 0 {
		dest[0] = '.'
		return 1, nil
	}
	if n > len(dest) {
		n = len(dest)
	}
	path := dest[:n]
	rooted := os.IsPathSeparator(path[0])
	w := 0
	r := 0
	dotdot := 0

	if rooted {
		dest[0] = os.PathSeparator
		w = 1
		r = 1
		dotdot = 1
	}

	for r < n {
		switch {
		case os.IsPathSeparator(path[r]):
			r++
		case path[r] == '.' && (r+1 == n || os.IsPathSeparator(path[r+1])):
			r++
		case path[r] == '.' && path[r+1] == '.' && (r+2 == n || os.IsPathSeparator(path[r+2])):
			r += 2
			if w > dotdot {
				w--
				for w > dotdot && !os.IsPathSeparator(dest[w]) {
					w--
				}
			} else if !rooted {
				if w > 0 {
					dest[w] = os.PathSeparator
					w++
				}
				dest[w] = '.'
				w++
				dest[w] = '.'
				w++
				dotdot = w
			}
		default:
			if rooted && w != 1 || !rooted && w != 0 {
				dest[w] = os.PathSeparator
				w++
			}
			for r < n && !os.IsPathSeparator(path[r]) {
				dest[w] = path[r]
				w++
				r++
			}
		}
	}
	if w == 0 {
		dest[0] = '.'
		w = 1
	}
	return w, nil
}
