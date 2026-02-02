package media

import (
	"bytes"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"

	"github.com/disintegration/imaging"

	// Register additional image formats
	_ "golang.org/x/image/webp"
)

// Quality levels to try (descending order)
var qualityLevels = []int{85, 75, 65, 55, 45, 35}

// Dimension levels to try if resizing needed (descending order)
var dimensionLevels = []int{2000, 1800, 1600, 1400, 1200, 1000, 800}

// Optimize resizes and compresses an image to meet Anthropic API limits.
// It returns the optimized ImageData or an error if optimization fails.
func Optimize(data []byte) (*ImageData, error) {
	// Detect MIME type
	mimeType := DetectMIME(data)
	if !IsSupported(mimeType) {
		return nil, fmt.Errorf("unsupported image type: %s", mimeType)
	}

	// Decode image
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to decode image: %w", err)
	}

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// Check if already within limits
	if width <= MaxDimension && height <= MaxDimension && len(data) <= MaxBytes {
		return &ImageData{
			Data:     data,
			MimeType: mimeType,
			Width:    width,
			Height:   height,
		}, nil
	}

	// Try to optimize with quality grid search
	return optimizeWithGridSearch(img, width, height, format, mimeType)
}

// optimizeWithGridSearch tries different dimension and quality combinations
// to find the smallest image that fits within limits.
func optimizeWithGridSearch(img image.Image, origWidth, origHeight int, format, origMimeType string) (*ImageData, error) {
	// Build list of dimensions to try
	maxDim := max(origWidth, origHeight)
	dimensions := make([]int, 0, len(dimensionLevels))
	for _, d := range dimensionLevels {
		if d <= MaxDimension && d < maxDim {
			dimensions = append(dimensions, d)
		}
	}
	// Prepend original dimension if within limits
	if maxDim <= MaxDimension {
		dimensions = append([]int{maxDim}, dimensions...)
	} else {
		dimensions = append([]int{MaxDimension}, dimensions...)
	}

	var smallest *ImageData

	for _, targetDim := range dimensions {
		// Resize if needed
		resized := img
		newWidth, newHeight := origWidth, origHeight
		if origWidth > targetDim || origHeight > targetDim {
			resized = imaging.Fit(img, targetDim, targetDim, imaging.Lanczos)
			bounds := resized.Bounds()
			newWidth = bounds.Dx()
			newHeight = bounds.Dy()
		}

		// Try different quality levels (for JPEG) or just encode (for PNG/GIF)
		for _, quality := range qualityLevels {
			encoded, mimeType, err := encodeImage(resized, format, quality)
			if err != nil {
				continue
			}

			// Track smallest result
			if smallest == nil || len(encoded) < len(smallest.Data) {
				smallest = &ImageData{
					Data:     encoded,
					MimeType: mimeType,
					Width:    newWidth,
					Height:   newHeight,
				}
			}

			// Found one within limits, return it
			if len(encoded) <= MaxBytes {
				return &ImageData{
					Data:     encoded,
					MimeType: mimeType,
					Width:    newWidth,
					Height:   newHeight,
				}, nil
			}
		}

		// For non-JPEG formats, only one encoding per dimension
		if format != "jpeg" {
			break
		}
	}

	// Return smallest found, even if over limit (caller can decide what to do)
	if smallest != nil {
		if len(smallest.Data) > MaxBytes {
			return nil, fmt.Errorf("image could not be reduced below %dMB (got %.2fMB)",
				MaxBytes/(1024*1024), float64(len(smallest.Data))/(1024*1024))
		}
		return smallest, nil
	}

	return nil, fmt.Errorf("failed to optimize image")
}

// encodeImage encodes an image in the specified format with given quality
func encodeImage(img image.Image, format string, quality int) ([]byte, string, error) {
	var buf bytes.Buffer

	switch format {
	case "jpeg":
		err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
		return buf.Bytes(), "image/jpeg", err

	case "png":
		// PNG doesn't have quality settings, just encode once
		err := png.Encode(&buf, img)
		return buf.Bytes(), "image/png", err

	case "gif":
		// GIF doesn't have quality settings
		err := gif.Encode(&buf, img, nil)
		return buf.Bytes(), "image/gif", err

	case "webp":
		// WebP input - convert to JPEG for output (Go's webp package is decode-only)
		err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
		return buf.Bytes(), "image/jpeg", err

	default:
		// Unknown format - try JPEG
		err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
		return buf.Bytes(), "image/jpeg", err
	}
}

// OptimizeFromFile reads and optimizes an image from disk
func OptimizeFromFile(path string) (*ImageData, error) {
	img, err := imaging.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open image: %w", err)
	}

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// Determine format from file
	format := "jpeg" // default

	// Encode to bytes first to check size
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: MaxQuality}); err != nil {
		return nil, fmt.Errorf("failed to encode image: %w", err)
	}

	// If within limits, return as-is
	if width <= MaxDimension && height <= MaxDimension && buf.Len() <= MaxBytes {
		return &ImageData{
			Data:     buf.Bytes(),
			MimeType: "image/jpeg",
			Width:    width,
			Height:   height,
		}, nil
	}

	// Otherwise, optimize
	return optimizeWithGridSearch(img, width, height, format, "image/jpeg")
}
