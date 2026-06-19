package dque

import (
	"os"
)

// dirExists returns true if the path exists and is a directory.
// On OS errors other than "not found", it returns the error.
func dirExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return info.IsDir(), nil
}

// fileExists returns true if the path exists and is not a directory.
// On OS errors other than "not found", it returns the error.
func fileExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return !info.IsDir(), nil
}
