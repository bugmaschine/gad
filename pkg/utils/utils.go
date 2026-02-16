package utils

import (
	"os"
)

// RemoveFileIgnoreNotExists removes a file, ignoring the error if it doesn't exist.
func RemoveFileIgnoreNotExists(path string) error {
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// RemoveDirAllIgnoreNotExists removes a directory and all its contents, ignoring the error if it doesn't exist.
func RemoveDirAllIgnoreNotExists(path string) error {
	err := os.RemoveAll(path)
	// os.RemoveAll handles non-existence gracefully and returns nil, but we check for clarity.
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
