// Package documentextract provides the `document_extract` agent tool.
//
// It turns user-uploaded documents (PDF, DOCX, PPTX, XLSX, EPUB, MOBI, HTML,
// plain text / markdown) into LLM-ready markdown via the go-markitdown
// library. Full extractions are cached under media/extracted/ keyed by
// (contentHash, flagsHash), so repeated calls for the same (document,
// options) are free. OCR and embedded image descriptions are routed through
// the agent vision chain via llm.AgentVisionDescriber.
//
// The tool returns a small structured preview — title, TOC, first 1500 chars,
// image count, output_path — and leaves the full markdown on disk. The agent
// can then follow up with the existing `read` tool against output_path to
// fetch more content or specific line ranges, avoiding a context-window blow
// up for large documents.
package documentextract

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/roelfdiedericks/go-markitdown/docconv"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// previewChars is the number of characters returned to the agent in the
// structured `preview` field. Large enough to give the model useful signal,
// small enough to keep tool results light. Agents fetch more via the `read`
// tool against output_path.
const previewChars = 1500

// defaultMaxVisionCalls bounds the number of OCR + image-description calls a
// single extraction can make against the agent vision chain. Protects against
// pathological documents (e.g. a 200-image PDF) exploding the LLM bill.
const defaultMaxVisionCalls = 50

// Tool implements the document_extract agent tool.
type Tool struct {
	store      *media.MediaStore
	registry   *llm.Registry
	workingDir string
}

// NewTool constructs a document_extract tool.
//
// store is used to locate the media root (cache is written to
// <baseDir>/extracted/); a nil store falls back to workingDir/media/extracted.
// registry routes ImageDescriber calls (OCR + inline image descriptions)
// through the agent vision chain; a nil registry disables vision features.
// workingDir is used to resolve relative `path` arguments.
func NewTool(store *media.MediaStore, registry *llm.Registry, workingDir string) *Tool {
	return &Tool{
		store:      store,
		registry:   registry,
		workingDir: workingDir,
	}
}

// Name returns the tool's registered name.
func (t *Tool) Name() string { return "document_extract" }

// Description is shown to the LLM so it can decide when to call this tool.
func (t *Tool) Description() string {
	return "Extract clean, LLM-ready markdown from a document (PDF, DOCX, XLSX, PPTX, EPUB, MOBI, HTML, markdown, plain text) via go-markitdown. " +
		"Returns a structured preview (title, TOC, first ~1500 chars) and writes the full markdown to media/extracted/ for later retrieval. " +
		"Use this whenever a user uploads or references a document you cannot otherwise read. " +
		"Optional OCR and embedded-image descriptions route through the configured agent vision chain. " +
		"For long documents, call this tool once, then use the `read` tool against the returned output_path to fetch specific sections."
}

// Schema is the JSON Schema the LLM uses to validate calls.
func (t *Tool) Schema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []string{"path"},
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the document. Accepts absolute paths, paths relative to the working directory, or media-store paths like ./media/uploads/... or media/...",
			},
			"ocr": map[string]any{
				"type":        "boolean",
				"default":     false,
				"description": "Run OCR via the agent vision chain on pages with no extractable text. Slow and costs API credits; strictly opt-in.",
			},
			"include_images": map[string]any{
				"type":        "boolean",
				"default":     false,
				"description": "Describe embedded images inline via the agent vision chain. Costs API credits per image.",
			},
			"metadata": map[string]any{
				"type":        "boolean",
				"default":     false,
				"description": "Prepend YAML front-matter with title / author / page count / created where the source exposes it.",
			},
			"max_vision_calls": map[string]any{
				"type":        "integer",
				"default":     defaultMaxVisionCalls,
				"description": "Cap on combined OCR + image-description calls for this extraction (0 = unlimited). Remaining images are left as references and reported in truncated_images.",
			},
			"force_refresh": map[string]any{
				"type":        "boolean",
				"default":     false,
				"description": "Bypass the cache and re-extract. Useful if the document on disk has been replaced or you suspect a stale cache.",
			},
		},
	}
}

type input struct {
	Path           string `json:"path"`
	OCR            bool   `json:"ocr"`
	IncludeImages  bool   `json:"include_images"`
	Metadata       bool   `json:"metadata"`
	MaxVisionCalls *int   `json:"max_vision_calls"`
	ForceRefresh   bool   `json:"force_refresh"`
}

// flagSet captures the subset of input parameters that actually change the
// output content. Cache keys are derived from a hash of this struct so two
// calls with different output-affecting flags never share a cache slot.
// max_vision_calls (caps work) and force_refresh (control signal) are
// excluded on purpose.
type flagSet struct {
	OCR           bool `json:"ocr"`
	IncludeImages bool `json:"include_images"`
	Metadata      bool `json:"metadata"`
}

type errorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
	Hint    string `json:"hint,omitempty"`
	Format  string `json:"format,omitempty"`
}

type output struct {
	OK              bool          `json:"ok"`
	Format          string        `json:"format,omitempty"`
	OutputPath      string        `json:"output_path,omitempty"`
	Bytes           int           `json:"bytes,omitempty"`
	Lines           int           `json:"lines,omitempty"`
	Title           string        `json:"title,omitempty"`
	TOC             []string      `json:"toc,omitempty"`
	Preview         string        `json:"preview,omitempty"`
	ImageCount      int           `json:"image_count,omitempty"`
	TruncatedImages int           `json:"truncated_images,omitempty"`
	Warnings        []string      `json:"warnings,omitempty"`
	Cached          bool          `json:"cached,omitempty"`
	Error           *errorPayload `json:"error,omitempty"`
}

// sidecar is the JSON metadata file written next to each cached markdown.
// It lets cached hits short-circuit without re-parsing the whole markdown
// body for title/TOC/image counts.
type sidecar struct {
	Format          string    `json:"format"`
	Title           string    `json:"title,omitempty"`
	TOC             []string  `json:"toc,omitempty"`
	ImageCount      int       `json:"image_count"`
	TruncatedImages int       `json:"truncated_images"`
	Warnings        []string  `json:"warnings,omitempty"`
	Bytes           int       `json:"bytes"`
	Lines           int       `json:"lines"`
	CreatedAt       time.Time `json:"created_at"`
}

// Execute is the tool entry point. It resolves path, computes cache keys,
// returns cached results where possible, or runs a full extraction, writes
// the result to the extracted/ media category, and returns a structured
// preview.
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (*types.ToolResult, error) {
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		return marshalError("invalid_input", "invalid input: "+err.Error(), "", "")
	}

	path := strings.TrimSpace(in.Path)
	if path == "" {
		return marshalError("invalid_input", "path is required", "", "")
	}

	absPath, err := t.resolvePath(path)
	if err != nil {
		L_warn("document_extract: path resolution failed", "path", path, "error", err)
		return marshalError("invalid_path", err.Error(), "", "")
	}

	content, err := os.ReadFile(absPath) // #nosec G304 -- path is resolved against workingDir/mediaRoot
	if err != nil {
		L_warn("document_extract: read failed", "path", absPath, "error", err)
		return marshalError("read_failed", err.Error(), "", "")
	}

	format, _, detectErr := docconv.DetectReader(strings.NewReader(string(content)))
	_ = detectErr // non-fatal: docconv.ExtractReaderContext will re-detect and return a typed error if the format is unsupported
	formatStr := format.String()

	contentHash := hashBytes(content)
	flagsHash, err := hashFlags(flagSet{OCR: in.OCR, IncludeImages: in.IncludeImages, Metadata: in.Metadata})
	if err != nil {
		return marshalError("internal_error", "failed to hash flags: "+err.Error(), "", "")
	}

	cacheDir := t.cacheDir()
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return marshalError("internal_error", "failed to create cache dir: "+err.Error(), "", "")
	}

	baseName := contentHash + "-" + flagsHash
	mdAbsPath := filepath.Join(cacheDir, baseName+".md")
	sidecarAbsPath := filepath.Join(cacheDir, baseName+".json")
	relOutput := relativeMediaPath(t.store, mdAbsPath)

	L_info("document_extract: invoked",
		"path", path,
		"format", formatStr,
		"ocr", in.OCR,
		"includeImages", in.IncludeImages,
		"metadata", in.Metadata,
		"forceRefresh", in.ForceRefresh,
		"contentBytes", len(content),
		"cacheBase", baseName,
	)

	if !in.ForceRefresh {
		if hit, err := loadCached(mdAbsPath, sidecarAbsPath); err == nil && hit != nil {
			L_info("document_extract: cache hit",
				"cacheBase", baseName,
				"format", hit.Format,
				"bytes", hit.Bytes,
			)
			return marshalOutput(output{
				OK:              true,
				Format:          hit.Format,
				OutputPath:      relOutput,
				Bytes:           hit.Bytes,
				Lines:           hit.Lines,
				Title:           hit.Title,
				TOC:             hit.TOC,
				Preview:         hit.PreviewFromFile(mdAbsPath),
				ImageCount:      hit.ImageCount,
				TruncatedImages: hit.TruncatedImages,
				Warnings:        hit.Warnings,
				Cached:          true,
			})
		}
	}

	maxVisionCalls := defaultMaxVisionCalls
	if in.MaxVisionCalls != nil {
		maxVisionCalls = *in.MaxVisionCalls
	}

	var warnings []string
	describer, describerWarning := t.buildDescriber(in, maxVisionCalls)
	if describerWarning != "" {
		warnings = append(warnings, describerWarning)
	}

	opts := &docconv.Options{
		IncludeImages:   in.IncludeImages,
		IncludeMetadata: in.Metadata,
		OCRFallback:     in.OCR,
		LLMClient:       describerAdapter(describer),
	}

	markdown, extractErr := docconv.ExtractReaderContext(ctx, strings.NewReader(string(content)), format, opts)
	if extractErr != nil {
		code, hint := classifyDocconvError(extractErr)
		L_warn("document_extract: extraction failed",
			"path", path,
			"format", formatStr,
			"errorCode", code,
			"error", extractErr,
		)
		return marshalError(code, extractErr.Error(), hint, formatStr)
	}

	imageCount := countMarkdownImages(markdown)
	truncated := 0
	if describer != nil {
		truncated = int(describer.truncated.Load())
		if truncated > 0 {
			warnings = append(warnings,
				fmt.Sprintf("vision-call cap reached: %d image(s) left as references without description (max_vision_calls=%d)",
					truncated, maxVisionCalls))
		}
	}

	title := extractTitle(markdown, in.Metadata)
	toc := extractTOC(markdown)

	if err := os.WriteFile(mdAbsPath, []byte(markdown), 0o600); err != nil {
		L_warn("document_extract: failed to write markdown cache", "path", mdAbsPath, "error", err)
		warnings = append(warnings, "failed to write cached markdown: "+err.Error())
	}
	lines := countLines(markdown)

	scCard := sidecar{
		Format:          formatStr,
		Title:           title,
		TOC:             toc,
		ImageCount:      imageCount,
		TruncatedImages: truncated,
		Warnings:        warnings,
		Bytes:           len(markdown),
		Lines:           lines,
		CreatedAt:       time.Now().UTC(),
	}
	if data, err := json.MarshalIndent(scCard, "", "  "); err == nil {
		if werr := os.WriteFile(sidecarAbsPath, data, 0o600); werr != nil {
			L_warn("document_extract: failed to write sidecar", "path", sidecarAbsPath, "error", werr)
		}
	}

	L_info("document_extract: extraction complete",
		"cacheBase", baseName,
		"format", formatStr,
		"bytes", len(markdown),
		"imageCount", imageCount,
		"truncatedImages", truncated,
		"warnings", len(warnings),
	)

	return marshalOutput(output{
		OK:              true,
		Format:          formatStr,
		OutputPath:      relOutput,
		Bytes:           len(markdown),
		Lines:           lines,
		Title:           title,
		TOC:             toc,
		Preview:         previewOf(markdown),
		ImageCount:      imageCount,
		TruncatedImages: truncated,
		Warnings:        warnings,
		Cached:          false,
	})
}

// resolvePath accepts absolute paths, paths relative to workingDir, and
// media-store paths (./media/... or media/...), matching how the existing
// `read` tool handles paths so the agent can pass either interchangeably.
func (t *Tool) resolvePath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}

	if t.store != nil {
		if strings.HasPrefix(p, "./media/") || strings.HasPrefix(p, "media/") {
			abs, err := media.ResolveMediaPath(t.store.BaseDir(), p)
			if err == nil {
				return abs, nil
			}
		}
	}

	if t.workingDir == "" {
		return filepath.Clean(p), nil
	}
	return filepath.Clean(filepath.Join(t.workingDir, p)), nil
}

func (t *Tool) cacheDir() string {
	if t.store != nil {
		return filepath.Join(t.store.BaseDir(), "extracted")
	}
	if t.workingDir != "" {
		return filepath.Join(t.workingDir, "media", "extracted")
	}
	return filepath.Join(os.TempDir(), "goclaw-extracted")
}

// buildDescriber returns a bounded describer for the extraction, or nil if
// vision features are disabled. The returned warning is appended to the tool
// response when the configuration is known-insufficient for the requested
// flags (e.g. OCR requested but no registry available).
func (t *Tool) buildDescriber(in input, maxVisionCalls int) (*boundedDescriber, string) {
	needsVision := in.OCR || in.IncludeImages
	if !needsVision {
		return nil, ""
	}
	if t.registry == nil {
		return nil, "ocr/include_images requested but no LLM registry configured; images will be left as references"
	}
	base := llm.NewAgentVisionDescriber(t.registry)
	return newBoundedDescriber(base, maxVisionCalls), ""
}

// boundedDescriber wraps an llm.AgentVisionDescriber with a hard cap on the
// number of DescribeImage calls it will forward. Beyond the cap every call
// returns an empty description and a "truncated" marker so docconv leaves the
// image as a reference.
type boundedDescriber struct {
	inner     *llm.AgentVisionDescriber
	max       int32
	called    atomic.Int32
	truncated atomic.Int32
}

func newBoundedDescriber(inner *llm.AgentVisionDescriber, maxCalls int) *boundedDescriber {
	var m int32
	switch {
	case maxCalls <= 0:
		m = 0
	case maxCalls > math.MaxInt32:
		m = math.MaxInt32
	default:
		m = int32(maxCalls) // #nosec G115 -- range-checked above
	}
	return &boundedDescriber{inner: inner, max: m}
}

// DescribeImage implements docconv.ImageDescriber. Wraps the underlying
// vision adapter with the per-extraction call cap.
func (b *boundedDescriber) DescribeImage(ctx context.Context, img []byte, mimeType, prompt string) (string, error) {
	if b == nil || b.inner == nil {
		return "", llm.ErrNoRegistry
	}
	if b.max > 0 && b.called.Load() >= b.max {
		b.truncated.Add(1)
		return "", fmt.Errorf("max_vision_calls reached (%d)", b.max)
	}
	b.called.Add(1)
	text, err := b.inner.DescribeImage(ctx, img, mimeType, prompt)
	if err != nil {
		if errors.Is(err, llm.ErrNoVisionModels) || errors.Is(err, llm.ErrNoRegistry) {
			b.truncated.Add(1)
		}
	}
	return text, err
}

// describerAdapter satisfies the docconv.ImageDescriber interface from a
// *boundedDescriber. A nil describer returns nil so docconv sees "no
// describer configured" and degrades gracefully.
func describerAdapter(d *boundedDescriber) docconv.ImageDescriber {
	if d == nil {
		return nil
	}
	return d
}

// ---------- Helpers ----------

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hashFlags(f flagSet) (string, error) {
	buf, err := json.Marshal(f)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:4]), nil // 8 hex chars
}

// relativeMediaPath returns the media-store-relative form of abs (./media/...
// prefix). Falls back to the absolute path if the file isn't under the store.
func relativeMediaPath(store *media.MediaStore, abs string) string {
	if store == nil {
		return abs
	}
	if rel := store.RelativePath(abs); rel != "" {
		return rel
	}
	return abs
}

// (hit *sidecar) PreviewFromFile reads the first previewChars characters of
// the cached markdown and returns them. Errors degrade to empty preview.
func (s *sidecar) PreviewFromFile(path string) string {
	if s == nil {
		return ""
	}
	f, err := os.Open(path) // #nosec G304 -- path is from the same cache dir we just wrote
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, previewChars)
	n, _ := f.Read(buf)
	return string(buf[:n])
}

func loadCached(mdPath, sidecarPath string) (*sidecar, error) {
	if _, err := os.Stat(mdPath); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(sidecarPath) // #nosec G304 -- same cache dir
	if err != nil {
		return nil, err
	}
	var sc sidecar
	if err := json.Unmarshal(data, &sc); err != nil {
		return nil, err
	}
	return &sc, nil
}

func previewOf(md string) string {
	if len(md) <= previewChars {
		return md
	}
	return md[:previewChars]
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	count := 1
	for _, c := range s {
		if c == '\n' {
			count++
		}
	}
	return count
}

var markdownImagePattern = regexp.MustCompile(`!\[[^\]]*\]\([^\)]+\)`)

func countMarkdownImages(md string) int {
	return len(markdownImagePattern.FindAllStringIndex(md, -1))
}

// extractTitle prefers the YAML front-matter title when metadata is enabled,
// falling back to the first H1 heading. Returns empty string if neither is
// available.
func extractTitle(md string, metadataEnabled bool) string {
	if metadataEnabled {
		if t := titleFromFrontMatter(md); t != "" {
			return t
		}
	}
	return firstH1(md)
}

var frontMatterTitle = regexp.MustCompile(`(?m)^title:\s*(.+)$`)

func titleFromFrontMatter(md string) string {
	if !strings.HasPrefix(md, "---\n") {
		return ""
	}
	end := strings.Index(md[4:], "\n---")
	if end < 0 {
		return ""
	}
	block := md[4 : 4+end]
	m := frontMatterTitle.FindStringSubmatch(block)
	if len(m) != 2 {
		return ""
	}
	return strings.Trim(strings.TrimSpace(m[1]), `"'`)
}

func firstH1(md string) string {
	sc := bufio.NewScanner(strings.NewReader(md))
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

// extractTOC returns the list of markdown headings in order of appearance.
// Each entry is the heading text without the leading # marks. Capped to the
// first 50 entries to keep tool output light.
func extractTOC(md string) []string {
	const maxEntries = 50
	var toc []string
	sc := bufio.NewScanner(strings.NewReader(md))
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "#") {
			continue
		}
		i := 0
		for i < len(line) && line[i] == '#' {
			i++
		}
		if i < 1 || i > 6 {
			continue
		}
		text := strings.TrimSpace(line[i:])
		if text == "" {
			continue
		}
		toc = append(toc, text)
		if len(toc) >= maxEntries {
			break
		}
	}
	return toc
}

// classifyDocconvError maps docconv's typed errors onto stable error codes
// and, where useful, actionable hints for the agent.
func classifyDocconvError(err error) (code, hint string) {
	switch {
	case errors.Is(err, docconv.ErrUnsupportedFormat):
		return "unsupported_format", ""
	case errors.Is(err, docconv.ErrNoText):
		return "no_text", "retry with ocr=true to transcribe scanned pages via the vision chain"
	case errors.Is(err, docconv.ErrPasswordProtected):
		return "password_protected", ""
	case errors.Is(err, docconv.ErrCorruptDocument):
		return "corrupt", ""
	case errors.Is(err, docconv.ErrFitzRequired):
		return "backend_missing", "this build was compiled without the fitz backend; rebuild without -tags nofitz"
	default:
		return "extraction_failed", ""
	}
}

func marshalOutput(v output) (*types.ToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return types.TextResult(string(b)), nil
}

func marshalError(code, msg, hint, format string) (*types.ToolResult, error) {
	out := output{
		OK: false,
		Error: &errorPayload{
			Code:    code,
			Message: msg,
			Hint:    hint,
			Format:  format,
		},
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return &types.ToolResult{
		Content: []types.ContentBlock{{Type: "text", Text: string(b)}},
		IsError: true,
	}, nil
}
