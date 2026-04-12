package localllm

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDownloadFileWithResume(t *testing.T) {
	content := []byte("hello resumable download")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := 0
		if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
			if !strings.HasPrefix(rangeHeader, "bytes=") {
				t.Fatalf("unexpected range header %q", rangeHeader)
			}
			offset, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rangeHeader, "bytes="), "-"))
			if err != nil {
				t.Fatalf("parse range offset: %v", err)
			}
			start = offset
			w.WriteHeader(http.StatusPartialContent)
		}
		_, _ = w.Write(content[start:])
	}))
	defer server.Close()

	dest := filepath.Join(t.TempDir(), "file.bin")
	if err := os.WriteFile(dest+".partial", content[:5], 0o644); err != nil {
		t.Fatalf("write partial file: %v", err)
	}

	if err := downloadFileWithResume(context.Background(), server.URL, dest); err != nil {
		t.Fatalf("downloadFileWithResume returned error: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("expected %q, got %q", string(content), string(got))
	}
}

func TestExtractTarGz(t *testing.T) {
	src := filepath.Join(t.TempDir(), "runtime.tar.gz")
	if err := os.WriteFile(src, tarGzEntries(t, []tarEntry{
		{name: "llama-b1234/llama-server", body: "#!/bin/sh\necho ok\n", mode: 0o755},
		{name: "llama-b1234/README.md", body: "docs", mode: 0o644},
		{name: "llama-b1234/libllama.so.0", linkname: "libllama.so.0.0.1234", typeflag: tar.TypeSymlink},
		{name: "llama-b1234/libllama.so.0.0.1234", body: "binary", mode: 0o755},
	}), 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "runtime")
	if err := extractTarGz(src, dest); err != nil {
		t.Fatalf("extractTarGz returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "llama-server")); err != nil {
		t.Fatalf("expected extracted binary: %v", err)
	}
	linkPath := filepath.Join(dest, "libllama.so.0")
	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("expected extracted symlink: %v", err)
	}
	if target != "libllama.so.0.0.1234" {
		t.Fatalf("unexpected symlink target %q", target)
	}
}

func TestExtractZip(t *testing.T) {
	src := filepath.Join(t.TempDir(), "runtime.zip")
	if err := os.WriteFile(src, zipBytes(t, map[string]string{
		"llama-b1234/llama-server.exe": "binary",
	}), 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "runtime")
	if err := extractZip(src, dest); err != nil {
		t.Fatalf("extractZip returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "llama-server.exe")); err != nil {
		t.Fatalf("expected extracted binary: %v", err)
	}
}

func TestDownloadRuntime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".tar.gz") {
			_, _ = w.Write(tarGzEntries(t, []tarEntry{
				{name: "llama-b1234/llama-server", body: "#!/bin/sh\necho ok\n", mode: 0o755},
			}))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	origUpstream := upstreamReleaseBase
	upstreamReleaseBase = server.URL
	t.Cleanup(func() { upstreamReleaseBase = origUpstream })
	t.Setenv("HOME", t.TempDir())

	got, err := DownloadRuntime(context.Background(), "b1234", OSLinux, ArchAMD64, BackendCPU)
	if err != nil {
		t.Fatalf("DownloadRuntime returned error: %v", err)
	}
	if !strings.HasSuffix(got, filepath.Join(".goclaw", "local", "bin", "llama.cpp", "b1234-linux-amd64-cpu", "llama-server")) {
		t.Fatalf("unexpected runtime path %q", got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("expected runtime binary: %v", err)
	}
}

func TestDownloadRuntimeRepairsMissingSharedLibraryLinks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	installDir, err := RuntimeInstallDir("b1234", OSLinux, ArchAMD64, BackendCPU)
	if err != nil {
		t.Fatalf("RuntimeInstallDir returned error: %v", err)
	}
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatalf("mkdir install dir: %v", err)
	}
	binaryPath, err := RuntimeBinaryPath("b1234", OSLinux, ArchAMD64, BackendCPU)
	if err != nil {
		t.Fatalf("RuntimeBinaryPath returned error: %v", err)
	}
	if err := os.WriteFile(binaryPath, []byte("binary"), 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	for _, name := range []string{
		"libmtmd.so.0.0.8742",
		"libllama.so.0.0.8742",
		"libggml.so.0.9.11",
		"libggml-base.so.0.9.11",
	} {
		if err := os.WriteFile(filepath.Join(installDir, name), []byte("lib"), 0o755); err != nil {
			t.Fatalf("write shared library %s: %v", name, err)
		}
	}

	got, err := DownloadRuntime(context.Background(), "b1234", OSLinux, ArchAMD64, BackendCPU)
	if err != nil {
		t.Fatalf("DownloadRuntime returned error: %v", err)
	}
	if got != binaryPath {
		t.Fatalf("expected cached runtime path %q, got %q", binaryPath, got)
	}
	for _, name := range []string{
		"libmtmd.so.0",
		"libllama.so.0",
		"libggml.so.0",
		"libggml-base.so.0",
	} {
		if _, err := os.Lstat(filepath.Join(installDir, name)); err != nil {
			t.Fatalf("expected repaired soname link %s: %v", name, err)
		}
	}
}

func TestDownloadManagedModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "gemma-4-e2b-it-Q8_0.gguf"):
			_, _ = w.Write([]byte("model"))
		case strings.Contains(r.URL.Path, "mmproj-gemma-4-e2b-it-bf16.gguf"):
			_, _ = w.Write([]byte("mmproj"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	origHFBase := huggingFaceBaseURL
	huggingFaceBaseURL = server.URL
	t.Cleanup(func() { huggingFaceBaseURL = origHFBase })
	t.Setenv("HOME", t.TempDir())

	spec, err := ManagedModelByID("gemma4-e2b")
	if err != nil {
		t.Fatalf("ManagedModelByID returned error: %v", err)
	}

	got, err := DownloadManagedModel(context.Background(), spec)
	if err != nil {
		t.Fatalf("DownloadManagedModel returned error: %v", err)
	}
	if !strings.HasSuffix(got, filepath.Join(".goclaw", "local", "models", spec.ID, spec.PreferredFilename)) {
		t.Fatalf("unexpected model path %q", got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("expected model file: %v", err)
	}
	mmprojPath, err := ManagedModelMMProjPath(spec)
	if err != nil {
		t.Fatalf("ManagedModelMMProjPath returned error: %v", err)
	}
	if _, err := os.Stat(mmprojPath); err != nil {
		t.Fatalf("expected mmproj file: %v", err)
	}
}

func TestDownloadManagedModelWithoutMMProj(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "Qwen3-Coder-Next-Q8_0.gguf") {
			_, _ = w.Write([]byte("model"))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	origHFBase := huggingFaceBaseURL
	huggingFaceBaseURL = server.URL
	t.Cleanup(func() { huggingFaceBaseURL = origHFBase })
	t.Setenv("HOME", t.TempDir())

	spec, err := ManagedModelByID("qwen3-coder-next")
	if err != nil {
		t.Fatalf("ManagedModelByID: %v", err)
	}

	got, err := DownloadManagedModel(context.Background(), spec)
	if err != nil {
		t.Fatalf("DownloadManagedModel: %v", err)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("expected model file: %v", err)
	}
	mmprojPath, err := ManagedModelMMProjPath(spec)
	if err != nil || mmprojPath != "" {
		t.Fatalf("expected no mmproj path, got %q err %v", mmprojPath, err)
	}
}

func tarGzBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	entries := make([]tarEntry, 0, len(files))
	for name, content := range files {
		entries = append(entries, tarEntry{name: name, body: content, mode: 0o755})
	}
	return tarGzEntries(t, entries)
}

type tarEntry struct {
	name     string
	body     string
	mode     int64
	typeflag byte
	linkname string
}

func tarGzEntries(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	for _, entry := range entries {
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		mode := entry.mode
		if mode == 0 {
			mode = 0o755
		}
		hdr := &tar.Header{
			Name:     entry.name,
			Mode:     mode,
			Typeflag: typeflag,
			Linkname: entry.linkname,
		}
		if typeflag == tar.TypeReg {
			hdr.Size = int64(len(entry.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(entry.body)); err != nil {
				t.Fatalf("write tar body: %v", err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return buf.Bytes()
}

func zipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		fw, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip entry: %v", err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatalf("write zip entry: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	return buf.Bytes()
}
