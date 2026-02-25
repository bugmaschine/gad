package utils

import (
	"os"
	"regexp"
	"strings"
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

func CleanFolderName(rawName string) string {
	// i had a script that used sdl to download stuff (basically the queue feature, but more manual), and to make it backwards compatible to that script, i made it clean the titles in a similar way.
	name := strings.TrimSpace(rawName)

	illegalChars := regexp.MustCompile(`[\\/:*?"<>|]`)
	name = illegalChars.ReplaceAllString(name, "")

	multiSpace := regexp.MustCompile(`\s+`)
	name = multiSpace.ReplaceAllString(name, " ")

	name = strings.Trim(name, ". ")

	return name
}
