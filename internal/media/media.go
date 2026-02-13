// Package media provides image processing utilities for GoClaw.
// It handles image optimization (resize, compress) for Anthropic API limits
// and MIME type detection from magic bytes.
package media

import (
	"encoding/base64"

	"github.com/gabriel-vasile/mimetype"
)

// Anthropic API limits for images
const (
	MaxDimension = 2000            // Max width or height in pixels
	MaxBytes     = 5 * 1024 * 1024 // 5MB max file size
	MinQuality   = 35              // Minimum JPEG quality to try
	MaxQuality   = 85              // Starting JPEG quality
)

// Supported image MIME types for Anthropic vision
var SupportedMIMETypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

// ImageData represents a processed image ready for the LLM
type ImageData struct {
	Data     []byte // Raw image bytes
	MimeType string // MIME type (e.g., "image/jpeg")
	Width    int    // Width in pixels
	Height   int    // Height in pixels
}

// Base64 returns the image data as a base64-encoded string
func (img *ImageData) Base64() string {
	return base64.StdEncoding.EncodeToString(img.Data)
}

// Size returns the size in bytes
func (img *ImageData) Size() int {
	return len(img.Data)
}

// IsWithinLimits returns true if the image meets Anthropic API limits
func (img *ImageData) IsWithinLimits() bool {
	return img.Width <= MaxDimension &&
		img.Height <= MaxDimension &&
		len(img.Data) <= MaxBytes
}

// DetectMIME returns the MIME type from magic bytes (not file extension)
func DetectMIME(data []byte) string {
	return mimetype.Detect(data).String()
}

// IsSupported returns true if the MIME type is supported by Anthropic vision
func IsSupported(mimeType string) bool {
	return SupportedMIMETypes[mimeType]
}

// ImageAttachment represents an image attached to a message
type ImageAttachment struct {
	Data     string `json:"data"`             // Base64-encoded image data
	MimeType string `json:"mimeType"`         // MIME type (e.g., "image/jpeg")
	Source   string `json:"source,omitempty"` // Source channel (e.g., "telegram", "browser")
	Width    int    `json:"width,omitempty"`  // Width in pixels (optional)
	Height   int    `json:"height,omitempty"` // Height in pixels (optional)
}

// NewImageAttachment creates an ImageAttachment from ImageData
func NewImageAttachment(img *ImageData, source string) *ImageAttachment {
	return &ImageAttachment{
		Data:     img.Base64(),
		MimeType: img.MimeType,
		Source:   source,
		Width:    img.Width,
		Height:   img.Height,
	}
}
