package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

// fetchCached retrieves data from url, using cachePath as a local cache.
// If refresh is true, always fetches from remote regardless of cache.
// If offline is true, only uses cache and fails if missing.
// Returns the raw bytes and any error.
func fetchCached(url, cachePath string, refresh, offline bool) ([]byte, error) {
	if offline {
		data, err := os.ReadFile(cachePath)
		if err != nil {
			return nil, fmt.Errorf("offline mode: cache miss for %s: %w", cachePath, err)
		}
		return data, nil
	}

	if !refresh {
		if data, err := os.ReadFile(cachePath); err == nil {
			return data, nil
		}
	}

	resp, err := httpClient.Get(url)
	if err != nil {
		// Network error — try cache fallback
		if data, err2 := os.ReadFile(cachePath); err2 == nil {
			fmt.Fprintf(os.Stderr, "WARN: fetch failed for %s, using cache: %v\n", url, err)
			return data, nil
		}
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		// Non-200 — try cache fallback
		if data, err2 := os.ReadFile(cachePath); err2 == nil {
			fmt.Fprintf(os.Stderr, "WARN: HTTP %d for %s, using cache\n", resp.StatusCode, url)
			return data, nil
		}
		return nil, fmt.Errorf("fetch %s: HTTP %d", url, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response from %s: %w", url, err)
	}

	if err := writeCache(cachePath, data); err != nil {
		fmt.Fprintf(os.Stderr, "WARN: failed to cache %s: %v\n", cachePath, err)
	}

	return data, nil
}

func writeCache(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
