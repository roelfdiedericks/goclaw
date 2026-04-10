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
	if err := os.WriteFile(src, tarGzBytes(t, map[string]string{
		"llama-b1234/llama-server": "#!/bin/sh\necho ok\n",
		"llama-b1234/README.md":    "docs",
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
			_, _ = w.Write(tarGzBytes(t, map[string]string{
				"llama-b1234/llama-server": "#!/bin/sh\necho ok\n",
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

func TestDownloadManagedModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "gemma-4-e2b-it-Q8_0.gguf"):
			_, _ = w.Write([]byte("model"))
		case strings.Contains(r.URL.Path, "mmproj-gemma-4-e2b-it-f16.gguf"):
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

func tarGzBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o755,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("write tar body: %v", err)
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
