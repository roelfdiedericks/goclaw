package localllm

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/paths"
)

var (
	httpClient          = http.DefaultClient
	huggingFaceBaseURL  = "https://huggingface.co"
)

func HuggingFaceResolveURL(repo, filename string) string {
	return fmt.Sprintf("%s/%s/resolve/main/%s?download=true",
		strings.TrimRight(huggingFaceBaseURL, "/"),
		strings.Trim(repo, "/"),
		neturl.PathEscape(strings.TrimLeft(filename, "/")),
	)
}

func DownloadRuntime(ctx context.Context, version string, osFlavor OSFlavor, arch Arch, backend Backend) (string, error) {
	binaryPath, err := RuntimeBinaryPath(version, osFlavor, arch, backend)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(binaryPath); err == nil {
		L_info("localllm: runtime already present", "binary", binaryPath)
		return binaryPath, nil
	}

	spec, err := ResolveLlamaCppArtifact(version, arch, osFlavor, backend)
	if err != nil {
		return "", err
	}

	installDir, err := RuntimeInstallDir(version, osFlavor, arch, backend)
	if err != nil {
		return "", err
	}
	if err := paths.EnsureDir(installDir); err != nil {
		return "", err
	}

	for _, name := range append(spec.AdditionalFiles, spec.Filename) {
		url := fmt.Sprintf("%s/%s", strings.TrimRight(spec.BaseURL, "/"), name)
		cachePath, err := runtimeDownloadCachePath(version, name)
		if err != nil {
			return "", err
		}
		if err := downloadAndInstallArchive(ctx, url, cachePath, installDir); err != nil {
			return "", err
		}
	}

	if _, err := os.Stat(binaryPath); err != nil {
		return "", fmt.Errorf("runtime download completed but binary missing at %s: %w", binaryPath, err)
	}

	L_info("localllm: runtime downloaded", "binary", binaryPath, "version", version, "backend", backend)
	return binaryPath, nil
}

func DownloadManagedModel(ctx context.Context, spec ManagedModelSpec) (string, error) {
	modelPath, err := ManagedModelPath(spec)
	if err != nil {
		return "", err
	}
	mmprojPath, err := ManagedModelMMProjPath(spec)
	if err != nil {
		return "", err
	}

	for _, item := range []struct {
		url  string
		path string
	}{
		{url: HuggingFaceResolveURL(spec.HFRepo, spec.PreferredFilename), path: modelPath},
		{url: HuggingFaceResolveURL(spec.HFRepo, spec.MMProjFilename), path: mmprojPath},
	} {
		if err := downloadFileWithResume(ctx, item.url, item.path); err != nil {
			return "", err
		}
	}

	L_info("localllm: model downloaded", "modelID", spec.ID, "path", modelPath)
	return modelPath, nil
}

func runtimeDownloadCachePath(version, filename string) (string, error) {
	layout, err := LocalStorageLayout()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(layout.DownloadsRootDir, "llama.cpp", version)
	if err := paths.EnsureDir(dir); err != nil {
		return "", err
	}
	return filepath.Join(dir, filename), nil
}

func downloadAndInstallArchive(ctx context.Context, url, cachePath, installDir string) error {
	if err := downloadFileWithResume(ctx, url, cachePath); err != nil {
		return err
	}

	switch {
	case strings.HasSuffix(cachePath, ".tar.gz"):
		return extractTarGz(cachePath, installDir)
	case strings.HasSuffix(cachePath, ".zip"):
		return extractZip(cachePath, installDir)
	default:
		return fmt.Errorf("unsupported archive type for %s", cachePath)
	}
}

func downloadFileWithResume(ctx context.Context, url, dest string) error {
	if _, err := os.Stat(dest); err == nil {
		L_debug("localllm: download already present", "path", dest)
		return nil
	}
	if err := paths.EnsureParentDir(dest); err != nil {
		return err
	}

	partial := dest + ".partial"
	startAt := int64(0)
	if info, err := os.Stat(partial); err == nil {
		startAt = info.Size()
	}

	file, err := os.OpenFile(partial, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := file.Seek(startAt, io.SeekStart); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if startAt > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", startAt))
	}

	L_info("localllm: downloading file", "url", url, "dest", dest, "resumeOffset", startAt)
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		if startAt > 0 {
			if err := file.Truncate(0); err != nil {
				return err
			}
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				return err
			}
		}
	case http.StatusPartialContent:
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("download failed with status %d: %s", resp.StatusCode, string(body))
	}

	if _, err := io.Copy(file, resp.Body); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(partial, dest); err != nil {
		return err
	}
	return nil
}

func extractTarGz(src, dest string) error {
	if err := paths.EnsureDir(dest); err != nil {
		return err
	}

	f, err := os.Open(filepath.Clean(src))
	if err != nil {
		return err
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		rel, ok := archiveRelativePath(header.Name)
		if !ok {
			continue
		}
		target, err := safeArchivePath(dest, rel)
		if err != nil {
			return err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := paths.EnsureParentDir(target); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		}
	}
}

func extractZip(src, dest string) error {
	if err := paths.EnsureDir(dest); err != nil {
		return err
	}

	r, err := zip.OpenReader(filepath.Clean(src))
	if err != nil {
		return err
	}
	defer r.Close()

	for _, file := range r.File {
		rel, ok := archiveRelativePath(file.Name)
		if !ok {
			continue
		}
		target, err := safeArchivePath(dest, rel)
		if err != nil {
			return err
		}

		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}

		if err := paths.EnsureParentDir(target); err != nil {
			return err
		}
		rc, err := file.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, file.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			rc.Close()
			out.Close()
			return err
		}
		if err := rc.Close(); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
	}
	return nil
}

func archiveRelativePath(name string) (string, bool) {
	clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(name)))
	if clean == "." || clean == "/" {
		return "", false
	}
	if idx := strings.Index(clean, "/"); idx >= 0 {
		clean = clean[idx+1:]
	}
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" || clean == "." {
		return "", false
	}
	return clean, true
}

func safeArchivePath(dest, rel string) (string, error) {
	target := filepath.Join(dest, filepath.FromSlash(rel))
	cleanDest := filepath.Clean(dest)
	cleanTarget := filepath.Clean(target)
	if cleanTarget != cleanDest && !strings.HasPrefix(cleanTarget, cleanDest+string(filepath.Separator)) {
		return "", fmt.Errorf("archive entry escapes destination: %s", rel)
	}
	return cleanTarget, nil
}
