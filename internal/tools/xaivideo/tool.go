package xaivideo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
	toolsconfig "github.com/roelfdiedericks/goclaw/internal/tools/config"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// Tool generates videos using xAI's video generation API.
type Tool struct {
	client     *videoClient
	config     toolsconfig.XAIVideoConfig
	mediaStore *media.MediaStore
}

// NewTool creates a new xAI video generation tool.
func NewTool(cfg toolsconfig.XAIVideoConfig, mediaStore *media.MediaStore) (*Tool, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("xai_video: API key required")
	}

	L_debug("xai_video: tool created",
		"model", cfg.Model,
		"resolution", cfg.Resolution,
		"duration", cfg.Duration,
		"pollInterval", cfg.PollInterval,
		"timeout", cfg.Timeout,
	)

	return &Tool{
		client:     newVideoClient(cfg.APIKey),
		config:     cfg,
		mediaStore: mediaStore,
	}, nil
}

func (t *Tool) Name() string {
	return "xai_video"
}

func (t *Tool) Description() string {
	return "Generate videos using xAI's Grok video generation. Supports text-to-video, image-to-video (animate still images), and video editing. Returns video path for delivery via {{media:path}}. Generation takes 1-5 minutes."
}

func (t *Tool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompt": map[string]any{
				"type":        "string",
				"description": "Video generation prompt. Be descriptive about motion, scene, and action.",
			},
			"duration": map[string]any{
				"type":        "integer",
				"description": "Video length in seconds (1-15). Default: 5",
			},
			"aspectRatio": map[string]any{
				"type":        "string",
				"description": "Aspect ratio: 1:1, 16:9, 9:16, 4:3, 3:4, 3:2, 2:3. Default: 16:9",
			},
			"resolution": map[string]any{
				"type":        "string",
				"description": "Resolution: 480p or 720p. Default: 480p",
			},
			"imageUrl": map[string]any{
				"type":        "string",
				"description": "URL of source image for image-to-video animation (optional). Must be publicly accessible.",
			},
			"videoUrl": map[string]any{
				"type":        "string",
				"description": "URL of source video for editing (optional). Max 8.7 seconds input. Editing ignores duration/resolution params.",
			},
		},
		"required": []string{"prompt"},
	}
}

type videoInput struct {
	Prompt      string `json:"prompt"`
	Duration    int    `json:"duration,omitempty"`
	AspectRatio string `json:"aspectRatio,omitempty"`
	Resolution  string `json:"resolution,omitempty"`
	ImageURL    string `json:"imageUrl,omitempty"`
	VideoURL    string `json:"videoUrl,omitempty"`
}

// getModel returns the model to use, with fallback to config default.
func (t *Tool) getModel() string {
	if t.config.Model != "" {
		return t.config.Model
	}
	return "grok-imagine-video"
}

// getDuration returns duration with config default fallback.
func (t *Tool) getDuration(input int) int {
	if input > 0 {
		if input > 15 {
			return 15
		}
		return input
	}
	if t.config.Duration > 0 {
		return t.config.Duration
	}
	return 5
}

// getResolution returns resolution with config default fallback.
func (t *Tool) getResolution(input string) string {
	if input != "" {
		return input
	}
	if t.config.Resolution != "" {
		return t.config.Resolution
	}
	return "480p"
}

// getPollInterval returns poll interval in seconds.
func (t *Tool) getPollInterval() int {
	if t.config.PollInterval > 0 {
		return t.config.PollInterval
	}
	return 5
}

// getTimeout returns timeout in seconds.
func (t *Tool) getTimeout() int {
	if t.config.Timeout > 0 {
		return t.config.Timeout
	}
	return 600
}

func (t *Tool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var params videoInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	if params.Prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}

	// Build request
	req := VideoRequest{
		Model:       t.getModel(),
		Prompt:      params.Prompt,
		Duration:    t.getDuration(params.Duration),
		AspectRatio: params.AspectRatio,
		Resolution:  t.getResolution(params.Resolution),
		ImageURL:    params.ImageURL,
		VideoURL:    params.VideoURL,
	}

	// Default aspect ratio if not specified
	if req.AspectRatio == "" {
		req.AspectRatio = "16:9"
	}

	promptPreview := params.Prompt
	if len(promptPreview) > 50 {
		promptPreview = promptPreview[:50] + "..."
	}

	L_info("xai_video: starting generation",
		"prompt", promptPreview,
		"duration", req.Duration,
		"resolution", req.Resolution,
		"aspectRatio", req.AspectRatio,
	)

	// Start generation
	requestID, err := t.client.Start(ctx, req)
	if err != nil {
		L_error("xai_video: failed to start generation", "error", err)
		return nil, fmt.Errorf("failed to start video generation: %w", err)
	}

	L_debug("xai_video: generation started", "requestID", requestID)

	// Poll for completion
	pollInterval := time.Duration(t.getPollInterval()) * time.Second
	timeout := time.Duration(t.getTimeout()) * time.Second

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()

	startTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()

		case <-timeoutTimer.C:
			L_error("xai_video: generation timed out", "requestID", requestID, "timeout", timeout)
			return nil, fmt.Errorf("video generation timed out after %s", timeout)

		case <-ticker.C:
			status, err := t.client.GetStatus(ctx, requestID)
			if err != nil {
				L_warn("xai_video: poll error", "error", err, "requestID", requestID)
				continue
			}

			elapsed := time.Since(startTime).Round(time.Second)

			switch status.Status {
			case "done":
				L_info("xai_video: generation complete", "requestID", requestID, "elapsed", elapsed)
				return t.handleComplete(ctx, status, params.Prompt)

			case "expired":
				L_error("xai_video: request expired", "requestID", requestID)
				return nil, fmt.Errorf("video generation request expired")

			default:
				L_debug("xai_video: still processing", "requestID", requestID, "elapsed", elapsed)
			}
		}
	}
}

func (t *Tool) handleComplete(ctx context.Context, status *StatusResponse, prompt string) (*types.ToolResult, error) {
	if status.Video == nil || status.Video.URL == "" {
		return nil, fmt.Errorf("no video URL in response")
	}

	// Determine saveToMedia setting
	saveToMedia := t.config.SaveToMedia
	if t.mediaStore == nil {
		saveToMedia = false
	}

	var relPath string
	var savedPath string

	if saveToMedia {
		absPath, rel, err := t.downloadVideo(ctx, status.Video.URL)
		if err != nil {
			L_warn("xai_video: failed to save video, returning URL", "error", err)
		} else {
			relPath = rel
			savedPath = absPath
		}
	}

	// Build result
	result := map[string]any{
		"duration": status.Video.Duration,
		"prompt":   prompt,
	}

	if relPath != "" {
		result["video"] = relPath
		L_info("xai_video: saved", "path", savedPath, "duration", status.Video.Duration)
	} else {
		result["url"] = status.Video.URL
		L_info("xai_video: returning URL", "duration", status.Video.Duration)
	}

	jsonResult, _ := json.Marshal(result)

	return &types.ToolResult{
		Content: []types.ContentBlock{
			types.TextBlock(string(jsonResult)),
		},
	}, nil
}

func (t *Tool) downloadVideo(ctx context.Context, url string) (absPath, relPath string, err error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("download failed: %s", resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}

	// Detect extension from content type
	ext := ".mp4"
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "webm") {
		ext = ".webm"
	}

	absPath, relPath, err = t.mediaStore.Save(data, "generated/video", ext)
	if err != nil {
		return "", "", err
	}

	return absPath, relPath, nil
}
