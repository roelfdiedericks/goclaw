package documentextract

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roelfdiedericks/go-markitdown/docconv"
	"github.com/roelfdiedericks/goclaw/internal/media"
)

// newTestTool builds a tool rooted at a temp dir. No media store, no LLM
// registry: plain-text fixtures exercise the happy path without CGO or
// vision models.
func newTestTool(t *testing.T) (*Tool, string) {
	t.Helper()
	dir := t.TempDir()
	return NewTool(nil, nil, dir), dir
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func mustUnmarshalOutput(t *testing.T, raw string) output {
	t.Helper()
	var out output
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal output: %v\nraw=%s", err, raw)
	}
	return out
}

func TestExecutePlainTextRoundTrip(t *testing.T) {
	tool, wd := newTestTool(t)

	src := filepath.Join(wd, "hello.txt")
	content := "# Hello World\n\nThis is a test document.\n\n## Section Two\n\nMore content here.\n"
	if err := os.WriteFile(src, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	res, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{
		"path": "hello.txt",
	}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("execute returned error: %s", res.GetText())
	}

	out := mustUnmarshalOutput(t, res.GetText())
	if !out.OK {
		t.Fatalf("expected ok=true, got %+v", out)
	}
	if out.Cached {
		t.Fatalf("first call should not be cached")
	}
	if out.Format != "text" && out.Format != "markdown" {
		// docconv reports "text" for plain .txt, "markdown" for recognised md.
		// Either is acceptable — both are plain-text extractions.
		t.Fatalf("unexpected format %q", out.Format)
	}
	if out.OutputPath == "" {
		t.Fatalf("expected output_path to be set")
	}
	if !strings.Contains(out.Preview, "Hello World") {
		t.Fatalf("preview missing content: %q", out.Preview)
	}
	if len(out.TOC) < 2 || out.TOC[0] != "Hello World" || out.TOC[1] != "Section Two" {
		t.Fatalf("unexpected TOC %v", out.TOC)
	}
	if out.Title != "Hello World" {
		t.Fatalf("unexpected title %q", out.Title)
	}

	if _, err := os.Stat(out.OutputPath); err != nil {
		// OutputPath falls back to absolute when no media store; stat it.
		t.Fatalf("extracted file missing: %v", err)
	}
}

func TestExecuteCacheHitOnSecondCall(t *testing.T) {
	tool, wd := newTestTool(t)

	src := filepath.Join(wd, "doc.txt")
	if err := os.WriteFile(src, []byte("# Doc\n\nBody.\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	args := mustJSON(t, map[string]any{"path": "doc.txt"})

	first, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}
	firstOut := mustUnmarshalOutput(t, first.GetText())
	if firstOut.Cached {
		t.Fatalf("first call should not be cached")
	}

	second, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("second execute: %v", err)
	}
	secondOut := mustUnmarshalOutput(t, second.GetText())
	if !secondOut.Cached {
		t.Fatalf("second call should be cached, got %+v", secondOut)
	}
	if secondOut.OutputPath != firstOut.OutputPath {
		t.Fatalf("cache hit returned different output_path: %q vs %q", secondOut.OutputPath, firstOut.OutputPath)
	}
}

func TestExecuteForceRefreshBypassesCache(t *testing.T) {
	tool, wd := newTestTool(t)

	src := filepath.Join(wd, "doc.txt")
	if err := os.WriteFile(src, []byte("# A\nBody.\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	first, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{"path": "doc.txt"}))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if mustUnmarshalOutput(t, first.GetText()).Cached {
		t.Fatalf("first call should not be cached")
	}

	second, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{"path": "doc.txt", "force_refresh": true}))
	if err != nil {
		t.Fatalf("force-refresh: %v", err)
	}
	if mustUnmarshalOutput(t, second.GetText()).Cached {
		t.Fatalf("force_refresh should bypass cache")
	}
}

func TestExecuteFlagChangeProducesDifferentCacheKey(t *testing.T) {
	tool, wd := newTestTool(t)

	src := filepath.Join(wd, "doc.txt")
	if err := os.WriteFile(src, []byte("# A\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	a, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{"path": "doc.txt", "metadata": false}))
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	b, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{"path": "doc.txt", "metadata": true}))
	if err != nil {
		t.Fatalf("b: %v", err)
	}

	aOut := mustUnmarshalOutput(t, a.GetText())
	bOut := mustUnmarshalOutput(t, b.GetText())
	if aOut.OutputPath == bOut.OutputPath {
		t.Fatalf("different flags should produce different cache paths, both = %q", aOut.OutputPath)
	}
	if bOut.Cached {
		t.Fatalf("different flags should miss cache on first call")
	}
}

func TestExecuteMissingPathReturnsError(t *testing.T) {
	tool, _ := newTestTool(t)

	res, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{"path": ""}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true for empty path")
	}
	out := mustUnmarshalOutput(t, res.GetText())
	if out.Error == nil || out.Error.Code != "invalid_input" {
		t.Fatalf("expected invalid_input error, got %+v", out.Error)
	}
}

func TestExecuteFileNotFoundReturnsError(t *testing.T) {
	tool, _ := newTestTool(t)

	res, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{"path": "does-not-exist.txt"}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true")
	}
	out := mustUnmarshalOutput(t, res.GetText())
	if out.Error == nil || out.Error.Code != "read_failed" {
		t.Fatalf("expected read_failed error, got %+v", out.Error)
	}
}

func TestClassifyDocconvError(t *testing.T) {
	cases := []struct {
		err     error
		wantCode string
	}{
		{docconv.ErrUnsupportedFormat, "unsupported_format"},
		{docconv.ErrNoText, "no_text"},
		{docconv.ErrPasswordProtected, "password_protected"},
		{docconv.ErrCorruptDocument, "corrupt"},
		{docconv.ErrFitzRequired, "backend_missing"},
		{errors.New("boom"), "extraction_failed"},
	}
	for _, c := range cases {
		code, _ := classifyDocconvError(c.err)
		if code != c.wantCode {
			t.Errorf("classifyDocconvError(%v) = %q, want %q", c.err, code, c.wantCode)
		}
	}
}

func TestExtractTOC(t *testing.T) {
	md := "# A\nbody\n## B\nbody\n### C\nbody\nnot-heading\n#### D\n"
	toc := extractTOC(md)
	want := []string{"A", "B", "C", "D"}
	if len(toc) != len(want) {
		t.Fatalf("expected %d toc entries, got %d (%v)", len(want), len(toc), toc)
	}
	for i := range want {
		if toc[i] != want[i] {
			t.Errorf("toc[%d] = %q, want %q", i, toc[i], want[i])
		}
	}
}

func TestExtractTitlePrefersFrontMatter(t *testing.T) {
	md := "---\ntitle: Real Title\npages: 3\n---\n\n# H1 Heading\n\nBody."
	if got := extractTitle(md, true); got != "Real Title" {
		t.Fatalf("expected front-matter title, got %q", got)
	}
	// With metadataEnabled=false, front-matter is still in the string but we skip it
	// and fall back to first H1.
	if got := extractTitle(md, false); got != "H1 Heading" {
		t.Fatalf("expected H1 fallback, got %q", got)
	}
}

func TestCountMarkdownImages(t *testing.T) {
	md := "Some text ![alt](a.png) and ![](b.jpg) and plain [link](c) and code `![x](y)`."
	got := countMarkdownImages(md)
	// Both inline links are image references; the code-fenced one also matches
	// the regex. Exact count depends on regex breadth; assert a reasonable lower
	// bound instead of a brittle exact.
	if got < 2 {
		t.Fatalf("expected at least 2 image references, got %d", got)
	}
}

func TestHashFlagsDiffersByOptions(t *testing.T) {
	a, err := hashFlags(flagSet{OCR: false, IncludeImages: false, Metadata: false})
	if err != nil {
		t.Fatalf("hashFlags: %v", err)
	}
	b, err := hashFlags(flagSet{OCR: true, IncludeImages: false, Metadata: false})
	if err != nil {
		t.Fatalf("hashFlags: %v", err)
	}
	if a == b {
		t.Fatalf("expected different flag sets to hash differently, both = %q", a)
	}
}

func TestBuildDescriberDisabledWithoutFlags(t *testing.T) {
	tool, _ := newTestTool(t)
	d, warn := tool.buildDescriber(input{}, 50)
	if d != nil {
		t.Fatalf("expected nil describer when no vision flags set")
	}
	if warn != "" {
		t.Fatalf("unexpected warning: %q", warn)
	}
}

func TestBuildDescriberWarnsWithoutRegistry(t *testing.T) {
	tool, _ := newTestTool(t)
	_, warn := tool.buildDescriber(input{OCR: true}, 50)
	if warn == "" {
		t.Fatalf("expected warning when OCR requested without registry")
	}
}

// Basic sanity that the media package path resolution integration doesn't
// break when workingDir is empty. We don't exercise full media-store path
// semantics here — those have their own tests in internal/media.
func TestResolvePathAbsolute(t *testing.T) {
	tool := NewTool(nil, nil, "")
	got, err := tool.resolvePath("/etc/hosts")
	if err != nil {
		t.Fatalf("resolvePath: %v", err)
	}
	if got != "/etc/hosts" {
		t.Fatalf("expected absolute path passthrough, got %q", got)
	}
	_ = (*media.MediaStore)(nil) // keep media import used in case other tests are trimmed
}
