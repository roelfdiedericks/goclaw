package update

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// extractTarGz extracts a .tar.gz archive to a destination directory
func extractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer func() { _ = gzr.Close() }()

	tr := tar.NewReader(gzr)

	// Clean and normalize the destination directory
	destDir = filepath.Clean(destDir)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read error: %w", err)
		}

		// Security: prevent path traversal
		// Clean the header name and ensure it doesn't escape destDir
		cleanName := filepath.Clean(header.Name)
		if strings.HasPrefix(cleanName, "..") || filepath.IsAbs(cleanName) {
			return fmt.Errorf("invalid tar path: %s", header.Name)
		}
		target := filepath.Join(destDir, cleanName)

		// Double-check: target must be within destDir
		if !strings.HasPrefix(target, destDir+string(os.PathSeparator)) && target != destDir {
			return fmt.Errorf("path escapes destination: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			// G301/G115: Permissions from archive are expected, 0755 is standard
			if err := os.MkdirAll(target, 0755); err != nil { //nolint:gosec
				return err
			}
		case tar.TypeReg:
			// Ensure parent directory exists
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil { //nolint:gosec
				return err
			}

			// Use sensible default permissions - executable if archive says so
			mode := os.FileMode(0644)
			if header.Mode&0111 != 0 {
				mode = 0755 //nolint:gosec
			}

			outFile, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR|os.O_TRUNC, mode)
			if err != nil {
				return err
			}

			// Limit extraction size to prevent decompression bombs
			limited := io.LimitReader(tr, 100*1024*1024) // 100MB max per file
			if _, err := io.Copy(outFile, limited); err != nil {
				outFile.Close()
				return err
			}
			outFile.Close()
		}
	}

	return nil
}
