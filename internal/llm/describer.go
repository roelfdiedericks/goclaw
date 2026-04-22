// Package llm provides unified LLM provider interfaces and implementations.
//
// describer.go implements docconv.ImageDescriber against the goclaw LLM
// registry, so the go-markitdown library can use goclaw's configured vision
// models (with failover + cooldown) to describe embedded images and OCR
// pages.
package llm

import (
	"context"
	"errors"
	"fmt"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// AgentVisionDescriber is a docconv.ImageDescriber implementation that routes
// image-description and OCR requests through a goclaw LLM model chain.
//
// Purpose defaults to "agent" when empty. Most vision-capable models live on
// the agent chain today; a dedicated purpose (e.g. "vision") can be added
// later without changing the adapter's shape.
type AgentVisionDescriber struct {
	Registry *Registry
	Purpose  string
}

// NewAgentVisionDescriber returns a describer backed by registry, using the
// "agent" purpose chain. A nil registry yields a describer that always fails
// with ErrNoRegistry, so callers can still wire the adapter without crashing
// in tests or during startup.
func NewAgentVisionDescriber(registry *Registry) *AgentVisionDescriber {
	return &AgentVisionDescriber{Registry: registry, Purpose: "agent"}
}

// ErrNoRegistry is returned by DescribeImage when the adapter has no
// registry attached.
var ErrNoRegistry = errors.New("llm: vision describer has no registry")

// DescribeImage implements docconv.ImageDescriber.
//
// Behaviour:
//   - Routes the request through Registry.DescribeImageWithFailover using
//     d.Purpose (default "agent").
//   - On ErrNoVisionModels (no vision-capable model in chain), logs once at
//     warn and returns the sentinel error unchanged so the caller (docconv)
//     can decide to leave image references in place instead of failing
//     extraction outright.
//   - Other errors are returned as-is with context.
func (d *AgentVisionDescriber) DescribeImage(
	ctx context.Context,
	img []byte,
	mimeType string,
	prompt string,
) (string, error) {
	if d == nil || d.Registry == nil {
		return "", ErrNoRegistry
	}

	purpose := d.Purpose
	if purpose == "" {
		purpose = "agent"
	}

	L_debug("describer: describe image",
		"purpose", purpose,
		"imageBytes", len(img),
		"mimeType", mimeType,
		"promptLen", len(prompt),
	)

	result, err := d.Registry.DescribeImageWithFailover(ctx, purpose, img, mimeType, prompt)
	if err != nil {
		if errors.Is(err, ErrNoVisionModels) {
			L_warn("describer: no vision models available, caller should degrade gracefully",
				"purpose", purpose)
			return "", err
		}
		L_error("describer: describe failed",
			"purpose", purpose,
			"mimeType", mimeType,
			"error", err,
		)
		return "", fmt.Errorf("describe image: %w", err)
	}

	if result == nil {
		return "", fmt.Errorf("describe image: nil result")
	}

	L_debug("describer: describe succeeded",
		"purpose", purpose,
		"model", result.ModelUsed,
		"textLen", len(result.Text),
		"failedOver", result.FailedOver,
	)
	return result.Text, nil
}
