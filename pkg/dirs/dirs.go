package dirs

import (
	"fmt"
	"os"
	"path/filepath"
)

// GetDataDir returns the path to the data directory, creating it if it doesn't exist.
func GetDataDir() (string, error) {
	var dataDir string

	// In Rust, dirs::data_dir() is ~/.local/share on Linux.
	// Go doesn't have a direct equivalent in os for this specific path across all platforms.
	// For now, we use os.UserConfigDir() as a sensible cross-platform alternative for app data.
	configDir, err := os.UserConfigDir()
	if err == nil {
		dataDir = filepath.Join(configDir, "gad")
	} else {
		// Fallback to executable location
		exePath, err := os.Executable()
		if err == nil {
			dataDir = filepath.Join(filepath.Dir(exePath), "gad-data")
		}
	}

	if dataDir == "" {
		return "", fmt.Errorf("failed to find data directory path")
	}

	err = os.MkdirAll(dataDir, 0755)
	if err != nil {
		return "", fmt.Errorf("failed to create data directory: %w", err)
	}

	return dataDir, nil
}

// GetSaveDirectory returns the directory where files should be saved.
func GetSaveDirectory(customSaveDirectory string) (string, error) {
	if customSaveDirectory != "" {
		return customSaveDirectory, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current working directory: %w", err)
	}
	return cwd, nil
}
