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
	"sort"
	"strconv"
	"strings"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/paths"
)

var (
	httpClient         = http.DefaultClient
	huggingFaceBaseURL = "https://huggingface.co"
)

const (
	downloadFilePerm     = 0o600
	archiveDirPerm       = 0o750
	maxArchiveEntryBytes = 8 << 30
	maxArchiveTotalBytes = 16 << 30
	maxArchiveInt64      = int64(^uint64(0) >> 1)
)

type DownloadProgress struct {
	Role            string
	Name            string
	FileIndex       int
	FileCount       int
	DownloadedBytes int64
	TotalBytes      int64
	Reusing         bool
}

type DownloadProgressFunc func(DownloadProgress)

type downloadByteProgressFunc func(downloadedBytes, totalBytes int64, reusing bool)

func HuggingFaceResolveURL(repo, filename string) string {
	return fmt.Sprintf("%s/%s/resolve/main/%s?download=true",
		strings.TrimRight(huggingFaceBaseURL, "/"),
		strings.Trim(repo, "/"),
		neturl.PathEscape(strings.TrimLeft(filename, "/")),
	)
}

func DownloadRuntime(ctx context.Context, version string, osFlavor OSFlavor, arch Arch, backend Backend, progress DownloadProgressFunc) (string, error) {
	binaryPath, err := RuntimeBinaryPath(version, osFlavor, arch, backend)
	if err != nil {
		return "", err
	}
	installDir, err := RuntimeInstallDir(version, osFlavor, arch, backend)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(binaryPath); err == nil {
		if err := reconcileRuntimeSharedLibraries(installDir, osFlavor); err != nil {
			return "", err
		}
		if progress != nil {
			progress(DownloadProgress{
				Role:            "runtime",
				Name:            filepath.Base(binaryPath),
				FileIndex:       1,
				FileCount:       1,
				DownloadedBytes: 1,
				TotalBytes:      1,
				Reusing:         true,
			})
		}
		L_info("localllm: runtime already present", "binary", binaryPath)
		return binaryPath, nil
	}

	spec, err := ResolveLlamaCppArtifact(version, arch, osFlavor, backend)
	if err != nil {
		return "", err
	}

	if err := paths.EnsureDir(installDir); err != nil {
		return "", err
	}

	files := append(spec.AdditionalFiles, spec.Filename)
	for i, name := range files {
		url := fmt.Sprintf("%s/%s", strings.TrimRight(spec.BaseURL, "/"), name)
		cachePath, err := runtimeDownloadCachePath(version, name)
		if err != nil {
			return "", err
		}
		fileProgress := withDownloadProgressMetadata(progress, "runtime", name, i+1, len(files))
		if err := downloadAndInstallArchive(ctx, url, cachePath, installDir, fileProgress); err != nil {
			return "", err
		}
	}

	if err := reconcileRuntimeSharedLibraries(installDir, osFlavor); err != nil {
		return "", err
	}

	if _, err := os.Stat(binaryPath); err != nil {
		return "", fmt.Errorf("runtime download completed but binary missing at %s: %w", binaryPath, err)
	}

	L_info("localllm: runtime downloaded", "binary", binaryPath, "version", version, "backend", backend)
	return binaryPath, nil
}

func DownloadManagedModel(ctx context.Context, spec ManagedModelSpec, progress DownloadProgressFunc) (string, error) {
	modelPath, err := ManagedModelPath(spec)
	if err != nil {
		return "", err
	}
	mmprojPath, err := ManagedModelMMProjPath(spec)
	if err != nil {
		return "", err
	}

	items := []struct {
		role     string
		filename string
		url      string
		path     string
	}{
		{role: "weights", filename: spec.PreferredFilename, url: HuggingFaceResolveURL(spec.HFRepo, spec.PreferredFilename), path: modelPath},
		{role: "mmproj", filename: spec.MMProjFilename, url: HuggingFaceResolveURL(spec.HFRepo, spec.MMProjFilename), path: mmprojPath},
	}
	downloadable := make([]struct {
		role     string
		filename string
		url      string
		path     string
	}, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.filename) == "" {
			continue
		}
		downloadable = append(downloadable, item)
	}
	for i, item := range downloadable {
		fileProgress := withDownloadProgressMetadata(progress, item.role, item.filename, i+1, len(downloadable))
		if err := downloadFileWithResume(ctx, item.url, item.path, fileProgress); err != nil {
			return "", fmt.Errorf("managed model %s %s file %q: %w", spec.ID, item.role, item.filename, err)
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

func downloadAndInstallArchive(ctx context.Context, url, cachePath, installDir string, progress downloadByteProgressFunc) error {
	if err := downloadFileWithResume(ctx, url, cachePath, progress); err != nil {
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

func downloadFileWithResume(ctx context.Context, url, dest string, progress downloadByteProgressFunc) error {
	if info, err := os.Stat(dest); err == nil {
		L_debug("localllm: download already present", "path", dest)
		if progress != nil {
			progress(info.Size(), info.Size(), true)
		}
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

	file, err := os.OpenFile(partial, os.O_CREATE|os.O_WRONLY, downloadFilePerm)
	if err != nil {
		return err
	}
	defer func() {
		if file != nil {
			_ = file.Close()
		}
	}()

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
			startAt = 0
		}
	case http.StatusPartialContent:
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 400 {
			snippet = snippet[:400] + "..."
		}
		L_warn("localllm: download HTTP error", "url", url, "dest", dest, "statusCode", resp.StatusCode, "bodySnippet", snippet)
		// Client errors (e.g. 404 wrong filename) will never succeed on resume; drop stale partial so dir matches reality.
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			if err := os.Remove(partial); err != nil && !os.IsNotExist(err) {
				L_debug("localllm: could not remove partial after HTTP 4xx", "path", partial, "error", err)
			}
		}
		return fmt.Errorf("download failed with status %d: %s", resp.StatusCode, string(body))
	}

	totalBytes := resp.ContentLength
	if resp.StatusCode == http.StatusPartialContent && totalBytes >= 0 {
		totalBytes += startAt
	}
	reader := newDownloadProgressReader(resp.Body, startAt, totalBytes, progress)
	if _, err := io.Copy(file, reader); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	file = nil
	if err := os.Rename(partial, dest); err != nil {
		return err
	}
	return nil
}

func withDownloadProgressMetadata(progress DownloadProgressFunc, role, name string, fileIndex, fileCount int) downloadByteProgressFunc {
	if progress == nil {
		return nil
	}
	return func(downloadedBytes, totalBytes int64, reusing bool) {
		progress(DownloadProgress{
			Role:            role,
			Name:            name,
			FileIndex:       fileIndex,
			FileCount:       fileCount,
			DownloadedBytes: downloadedBytes,
			TotalBytes:      totalBytes,
			Reusing:         reusing,
		})
	}
}

type downloadProgressReader struct {
	base        io.Reader
	current     int64
	total       int64
	lastEmit    time.Time
	lastPercent int
	progress    downloadByteProgressFunc
}

func newDownloadProgressReader(base io.Reader, startAt, totalBytes int64, progress downloadByteProgressFunc) *downloadProgressReader {
	r := &downloadProgressReader{
		base:        base,
		current:     startAt,
		total:       totalBytes,
		lastPercent: -1,
		progress:    progress,
	}
	r.emit(true)
	return r
}

func (r *downloadProgressReader) Read(p []byte) (int, error) {
	n, err := r.base.Read(p)
	if n > 0 {
		r.current += int64(n)
		r.emit(false)
	}
	if err == io.EOF {
		r.emit(true)
	}
	return n, err
}

func (r *downloadProgressReader) emit(force bool) {
	if r.progress == nil {
		return
	}
	now := time.Now()
	if !force {
		if r.total > 0 {
			percent := int((r.current * 100) / r.total)
			if percent == r.lastPercent && now.Sub(r.lastEmit) < 300*time.Millisecond {
				return
			}
			r.lastPercent = percent
		} else if now.Sub(r.lastEmit) < 300*time.Millisecond {
			return
		}
	}
	r.lastEmit = now
	r.progress(r.current, r.total, false)
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
	defer func() { _ = gzr.Close() }()

	tr := tar.NewReader(gzr)
	totalExtracted := int64(0)
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
			if err := os.MkdirAll(target, archiveDirPerm); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := paths.EnsureParentDir(target); err != nil {
				return err
			}
			if _, err := os.Lstat(target); err == nil {
				if err := os.Remove(target); err != nil {
					return err
				}
			} else if !os.IsNotExist(err) {
				return err
			}
			if err := os.Symlink(header.Linkname, target); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := paths.EnsureParentDir(target); err != nil {
				return err
			}
			if header.Size < 0 {
				return fmt.Errorf("archive entry %s has negative size", header.Name)
			}
			if header.Size > maxArchiveEntryBytes {
				return fmt.Errorf("archive entry %s exceeds size limit", header.Name)
			}
			if totalExtracted+header.Size > maxArchiveTotalBytes {
				return fmt.Errorf("archive extraction exceeds total size limit")
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, archiveFileMode(header.Mode))
			if err != nil {
				return err
			}
			if err := copyExactArchiveBytes(out, tr, header.Size); err != nil {
				out.Close()
				return err
			}
			totalExtracted += header.Size
			if err := out.Close(); err != nil {
				return err
			}
		}
	}
}

func reconcileRuntimeSharedLibraries(installDir string, osFlavor OSFlavor) error {
	if osFlavor != OSLinux && osFlavor != OSBookworm && osFlavor != OSTrixie {
		return nil
	}
	entries, err := os.ReadDir(installDir)
	if err != nil {
		return err
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		files = append(files, entry.Name())
	}
	sort.Strings(files)

	for _, name := range files {
		if !strings.Contains(name, ".so.") {
			continue
		}
		fullPath := filepath.Join(installDir, name)
		info, err := os.Lstat(fullPath)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || info.IsDir() {
			continue
		}
		target := sonameAliasName(name)
		if target == "" || target == name {
			continue
		}
		targetPath := filepath.Join(installDir, target)
		if _, err := os.Lstat(targetPath); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.Symlink(name, targetPath); err != nil {
			return err
		}
	}
	return nil
}

func sonameAliasName(name string) string {
	idx := strings.Index(name, ".so.")
	if idx < 0 {
		return ""
	}
	suffix := name[idx+len(".so."):]
	if suffix == "" {
		return ""
	}
	parts := strings.Split(suffix, ".")
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	return name[:idx+len(".so.")] + parts[0]
}

func extractZip(src, dest string) error {
	if err := paths.EnsureDir(dest); err != nil {
		return err
	}

	r, err := zip.OpenReader(filepath.Clean(src))
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()

	totalExtracted := int64(0)
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
			if err := os.MkdirAll(target, archiveDirPerm); err != nil {
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
		if file.UncompressedSize64 > uint64(maxArchiveInt64) {
			_ = rc.Close()
			return fmt.Errorf("archive entry %s exceeds supported size", file.Name)
		}
		entrySize, err := strconv.ParseInt(strconv.FormatUint(file.UncompressedSize64, 10), 10, 64)
		if err != nil {
			_ = rc.Close()
			return fmt.Errorf("archive entry %s size conversion failed: %w", file.Name, err)
		}
		if entrySize > maxArchiveEntryBytes {
			_ = rc.Close()
			return fmt.Errorf("archive entry %s exceeds size limit", file.Name)
		}
		if totalExtracted+entrySize > maxArchiveTotalBytes {
			_ = rc.Close()
			return fmt.Errorf("archive extraction exceeds total size limit")
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, file.Mode())
		if err != nil {
			_ = rc.Close()
			return err
		}
		if err := copyExactArchiveBytes(out, rc, entrySize); err != nil {
			_ = rc.Close()
			out.Close()
			return err
		}
		totalExtracted += entrySize
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

func archiveFileMode(mode int64) os.FileMode {
	if mode&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

func copyExactArchiveBytes(dst io.Writer, src io.Reader, size int64) error {
	if size < 0 {
		return fmt.Errorf("negative archive size")
	}
	written, err := io.CopyN(dst, src, size)
	if err != nil {
		return err
	}
	if written != size {
		return io.ErrUnexpectedEOF
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
