package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/roelfdiedericks/xai-go"
	"github.com/roelfdiedericks/goclaw/internal/config"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
)

// XAIImagineTool generates images using xAI's image generation API
type XAIImagineTool struct {
	client     *xai.Client
	config     config.XAIImagineConfig
	mediaStore *media.MediaStore
}

// NewXAIImagineTool creates a new xAI image generation tool
func NewXAIImagineTool(cfg config.XAIImagineConfig, mediaStore *media.MediaStore) (*XAIImagineTool, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("xai_imagine: API key required")
	}

	client, err := xai.New(xai.Config{
		APIKey: xai.NewSecureString(cfg.APIKey),
	})
	if err != nil {
		return nil, fmt.Errorf("xai_imagine: failed to create client: %w", err)
	}

	L_debug("xai_imagine: tool created",
		"model", cfg.Model,
		"resolution", cfg.Resolution,
		"saveToMedia", cfg.SaveToMedia,
	)

	return &XAIImagineTool{
		client:     client,
		config:     cfg,
		mediaStore: mediaStore,
	}, nil
}

func (t *XAIImagineTool) Name() string {
	return "xai_imagine"
}

func (t *XAIImagineTool) Description() string {
	return "Generate images using xAI's Grok image generation. Returns URLs to generated images. Use for creating illustrations, diagrams, artwork, etc."
}

func (t *XAIImagineTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompt": map[string]any{
				"type":        "string",
				"description": "The image generation prompt. Be descriptive about what you want to see.",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Model to use (optional). Default: grok-2-image",
			},
			"aspectRatio": map[string]any{
				"type":        "string",
				"description": "Aspect ratio (optional). Options: 1:1, 16:9, 9:16, 4:3, 3:4. Default: 1:1",
			},
			"resolution": map[string]any{
				"type":        "string",
				"description": "Image resolution (optional). Options: 1K (~1024px), 2K (~2048px upscaled). Default: 1K",
			},
			"count": map[string]any{
				"type":        "integer",
				"description": "Number of images to generate (optional). Default: 1, Max: 4",
			},
			"saveToMedia": map[string]any{
				"type":        "boolean",
				"description": "Save images to media store for delivery (optional). Default: true",
			},
			"inputImage": map[string]any{
				"type":        "string",
				"description": "URL of source image for image-to-image transformation (optional). Must be a publicly accessible URL.",
			},
		},
		"required": []string{"prompt"},
	}
}

type xaiImagineInput struct {
	Prompt      string `json:"prompt"`
	Model       string `json:"model,omitempty"`
	AspectRatio string `json:"aspectRatio,omitempty"`
	Resolution  string `json:"resolution,omitempty"`
	Count       int    `json:"count,omitempty"`
	SaveToMedia *bool  `json:"saveToMedia,omitempty"` // Pointer to distinguish unset from false
	InputImage  string `json:"inputImage,omitempty"`  // URL for image-to-image transformation
}

// getModel returns the model to use, with fallback to config and default
func (t *XAIImagineTool) getModel(input string) string {
	if input != "" {
		return input
	}
	if t.config.Model != "" {
		return t.config.Model
	}
	return "grok-2-image"
}

// parseAspectRatio converts aspect ratio string to xai.ImageAspectRatio
func parseAspectRatio(ratio string) *xai.ImageAspectRatio {
	if ratio == "" {
		return nil
	}
	switch ratio {
	case "1:1":
		ar := xai.ImageAspectRatio1x1
		return &ar
	case "16:9":
		ar := xai.ImageAspectRatio16x9
		return &ar
	case "9:16":
		ar := xai.ImageAspectRatio9x16
		return &ar
	case "4:3":
		ar := xai.ImageAspectRatio4x3
		return &ar
	case "3:4":
		ar := xai.ImageAspectRatio3x4
		return &ar
	default:
		return nil
	}
}

// parseResolution converts resolution string to xai.ImageResolution
// xAI supports: 1K (~1024px, default) and 2K (~2048px, upscaled)
func parseResolution(res string) *xai.ImageResolution {
	if res == "" {
		return nil
	}
	switch res {
	case "1K", "1k", "1024":
		r := xai.ImageResolution1K
		return &r
	case "2K", "2k", "2048":
		r := xai.ImageResolution2K
		return &r
	default:
		return nil
	}
}

func (t *XAIImagineTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params xaiImagineInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if params.Prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}

	// Build request
	req := xai.NewImageRequest(params.Prompt).
		WithModel(t.getModel(params.Model))

	// Apply aspect ratio if specified
	if ar := parseAspectRatio(params.AspectRatio); ar != nil {
		req.WithAspectRatio(*ar)
	}

	// Apply resolution: input param > config > default
	resStr := params.Resolution
	if resStr == "" {
		resStr = t.config.Resolution
	}
	if r := parseResolution(resStr); r != nil {
		req.WithResolution(*r)
	}

	// Apply input image for image-to-image transformation
	if params.InputImage != "" {
		req.WithInputImage(params.InputImage)
	}

	// Apply count (default 1, max 4)
	count := params.Count
	if count <= 0 {
		count = 1
	}
	if count > 4 {
		count = 4
	}
	req.WithCount(int32(count))

	// Determine saveToMedia: input param > config
	saveToMedia := t.config.SaveToMedia
	if params.SaveToMedia != nil {
		saveToMedia = *params.SaveToMedia
	}

	L_debug("xai_imagine: generating",
		"prompt", params.Prompt[:min(50, len(params.Prompt))],
		"model", t.getModel(params.Model),
		"aspectRatio", params.AspectRatio,
		"resolution", resStr,
		"count", count,
		"saveToMedia", saveToMedia,
	)

	// Execute request
	resp, err := t.client.GenerateImage(ctx, req)
	if err != nil {
		return "", fmt.Errorf("image generation failed: %w", err)
	}

	if len(resp.Images) == 0 {
		return "", fmt.Errorf("no images generated")
	}

	// Process results
	var results []string
	for i, img := range resp.Images {
		if img.URL == "" {
			continue
		}

		// Download and save to media store if enabled
		if saveToMedia && t.mediaStore != nil {
			localPath, err := t.downloadAndSave(ctx, img.URL, i)
			if err != nil {
				L_warn("xai_imagine: failed to save image", "error", err)
				results = append(results, fmt.Sprintf("Image %d: %s", i+1, img.URL))
			} else {
				// Use MEDIA: prefix for automatic delivery
				results = append(results, fmt.Sprintf("MEDIA:%s", localPath))
			}
		} else {
			results = append(results, fmt.Sprintf("Image %d: %s", i+1, img.URL))
		}
	}

	L_info("xai_imagine: generated",
		"count", len(resp.Images),
		"saved", saveToMedia,
	)

	// Build output with summary line
	summary := fmt.Sprintf("Generated %d image(s):", len(results))
	output := summary + "\n" + strings.Join(results, "\n")
	return output, nil
}

// downloadAndSave downloads an image URL and saves it to the media store
func (t *XAIImagineTool) downloadAndSave(ctx context.Context, url string, index int) (string, error) {
	// Create HTTP request with context
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: %s", resp.Status)
	}

	// Read body
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Detect content type and extension
	contentType := resp.Header.Get("Content-Type")
	ext := ".png"
	if strings.Contains(contentType, "jpeg") || strings.Contains(contentType, "jpg") {
		ext = ".jpg"
	} else if strings.Contains(contentType, "webp") {
		ext = ".webp"
	}

	// Save to media store using Save(data, subdir, ext)
	// Returns (absPath, relPath, error) - we return relPath for MEDIA: prefix
	_, relPath, err := t.mediaStore.Save(data, "generated", ext)
	if err != nil {
		return "", err
	}
	return relPath, nil
}
