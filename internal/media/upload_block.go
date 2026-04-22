package media

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/types"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// UploadMeta describes a user-uploaded file. Channels fill this in and pass the
// bytes to BuildUploadBlock, which returns a ready-to-dispatch ContentBlock.
type UploadMeta struct {
	// Channel is the source channel identifier: "telegram", "whatsapp", "http".
	// Also used as the UploadContext channel and, if Source is empty, as the
	// ContentBlock.Source tag.
	Channel string

	// Source is the ContentBlock.Source tag. Defaults to Channel when empty.
	Source string

	// User is the authenticated user (nil for anonymous HTTP uploads).
	User *user.User

	// ChannelUserID is the channel-specific user id (e.g. telegram numeric id).
	ChannelUserID string

	// ChatID is the session / chat identifier.
	ChatID string

	// OriginalName is the filename the user sent, if any.
	OriginalName string

	// ProvidedMime is the MIME type reported by the channel, if any. Empty means
	// the helper will fall back to magic-byte detection.
	ProvidedMime string

	// Caption is the free-form caption attached to the upload.
	Caption string

	// Duration is only meaningful for audio/video uploads where the channel
	// reports it; otherwise zero.
	Duration int

	// IsVoiceNote is the channel's intent signal for push-to-talk / voice note
	// audio. When true the helper emits an "audio" ContentBlock which the
	// gateway then routes through STT. Channels are responsible for this
	// classification; the helper never infers it from MIME.
	IsVoiceNote bool

	// ForceMediaType overrides the media-store subdirectory name (cosmetic only).
	// When empty, the helper derives it from the MIME prefix:
	// image/* -> "image", audio/* -> "audio", video/* -> "video", else "document".
	ForceMediaType string
}

// BuildUploadBlock saves uploaded bytes to the media store under
// uploads/{channel}/{user}/{mediatype}/ and returns a ContentBlock ready to be
// attached to a gateway.AgentRequest.
//
// Block-type rule (channel-agnostic):
//
//	IsVoiceNote            -> "audio"  (gateway runs STT)
//	image/* MIME           -> "image"
//	otherwise              -> "file"
//
// MIME resolution order: ProvidedMime, DetectMIME(data), "application/octet-stream".
// Extension comes from OriginalName when set, else mapped from MIME.
//
// Returns the built ContentBlock plus the absolute path the bytes were written
// to (useful for logging / tests).
func BuildUploadBlock(store *MediaStore, data []byte, meta UploadMeta) (types.ContentBlock, string, error) {
	if store == nil {
		return types.ContentBlock{}, "", fmt.Errorf("media: nil store")
	}
	if len(data) == 0 {
		return types.ContentBlock{}, "", fmt.Errorf("media: empty upload data")
	}

	mime := strings.TrimSpace(meta.ProvidedMime)
	if mime == "" {
		mime = DetectMIME(data)
	}
	if mime == "" {
		mime = "application/octet-stream"
	}

	ext := extensionFor(meta.OriginalName, mime)

	mediaType := meta.ForceMediaType
	if mediaType == "" {
		mediaType = mediaTypeFromMime(mime)
	}

	source := meta.Source
	if source == "" {
		source = meta.Channel
	}

	absPath, _, err := store.SaveUpload(data, ext, UploadContext{
		Channel:       meta.Channel,
		User:          meta.User,
		ChannelUserID: meta.ChannelUserID,
		ChatID:        meta.ChatID,
		MediaType:     mediaType,
		Caption:       meta.Caption,
		OriginalName:  meta.OriginalName,
	})
	if err != nil {
		return types.ContentBlock{}, "", fmt.Errorf("media: save upload: %w", err)
	}

	block := types.ContentBlock{
		FilePath: absPath,
		MimeType: mime,
		Source:   source,
	}

	switch {
	case meta.IsVoiceNote:
		block.Type = "audio"
		block.Duration = meta.Duration
	case strings.HasPrefix(mime, "image/"):
		block.Type = "image"
	default:
		block.Type = "file"
		if meta.OriginalName != "" {
			block.FileName = meta.OriginalName
		}
	}

	logging.L_debug("media: upload block built",
		"channel", meta.Channel,
		"blockType", block.Type,
		"mime", mime,
		"mediaType", mediaType,
		"size", len(data),
		"path", absPath,
		"voice", meta.IsVoiceNote,
	)

	return block, absPath, nil
}

// mediaTypeFromMime picks the media-store subdirectory label from a MIME prefix.
// This is cosmetic only (it just controls the folder name); callers that want a
// specific subdir should set UploadMeta.ForceMediaType.
func mediaTypeFromMime(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return "image"
	case strings.HasPrefix(mime, "audio/"):
		return "audio"
	case strings.HasPrefix(mime, "video/"):
		return "video"
	default:
		return "document"
	}
}

// extensionFor picks a filename extension, preferring the original upload
// filename when present and falling back to a MIME-based mapping.
func extensionFor(originalName, mime string) string {
	if originalName != "" {
		if ext := strings.ToLower(filepath.Ext(originalName)); ext != "" {
			return ext
		}
	}
	return extForMIME(mime)
}

// extForMIME maps common MIME types to a canonical extension. Unknown types
// fall back to ".bin".
func extForMIME(mime string) string {
	mime = strings.ToLower(strings.TrimSpace(mime))
	if idx := strings.Index(mime, ";"); idx >= 0 {
		mime = strings.TrimSpace(mime[:idx])
	}
	switch mime {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/heic":
		return ".heic"
	case "image/heif":
		return ".heif"
	case "audio/ogg", "audio/opus":
		return ".ogg"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/mp4", "audio/aac", "audio/x-m4a":
		return ".m4a"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/flac":
		return ".flac"
	case "video/mp4":
		return ".mp4"
	case "video/quicktime":
		return ".mov"
	case "video/webm":
		return ".webm"
	case "application/pdf":
		return ".pdf"
	case "application/zip":
		return ".zip"
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return ".docx"
	case "application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return ".pptx"
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return ".xlsx"
	case "application/msword":
		return ".doc"
	case "application/epub+zip":
		return ".epub"
	case "text/plain":
		return ".txt"
	case "text/html":
		return ".html"
	case "text/markdown":
		return ".md"
	case "application/json":
		return ".json"
	case "application/x-tgsticker":
		return ".tgs"
	}
	return ".bin"
}
