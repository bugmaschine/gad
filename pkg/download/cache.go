package download

import (
	"os"
	"sync"
)

type DirectoryCache struct {
	mu    sync.RWMutex
	files map[string]struct{}
}

func NewDirectoryCache(dir string) (*DirectoryCache, error) {
	cache := &DirectoryCache{
		files: make(map[string]struct{}),
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return cache, nil
		}
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			cache.files[entry.Name()] = struct{}{}
		}
	}

	return cache, nil
}

func (c *DirectoryCache) CheckIfEpisodeExists(name string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Check with .mp4 and .ts as in Rust code (implicitly handled by checking common names)
	if _, ok := c.files[name+".mp4"]; ok {
		return true
	}
	if _, ok := c.files[name+".ts"]; ok {
		return true
	}
	if _, ok := c.files[name]; ok {
		return true
	}

	return false
}

func (c *DirectoryCache) HasPrefix(prefix string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for f := range c.files {
		if len(f) >= len(prefix) && f[:len(prefix)] == prefix {
			// If the next character is a digit, then it's a collision (e.g. S01E10 matching S01E105)
			if len(f) > len(prefix) {
				nextChar := f[len(prefix)]
				if nextChar >= '0' && nextChar <= '9' {
					continue
				}
			}
			return true
		}
	}
	return false
}
