package media

import (
	"bytes"
	"image"
	"image/jpeg"
	"strings"
	"testing"
)

func newTestStore(t *testing.T) *MediaStore {
	t.Helper()
	store, err := NewMediaStore(MediaConfig{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewMediaStore: %v", err)
	}
	t.Cleanup(store.Close)
	return store
}

// makeJPEG builds a tiny real JPEG so DetectMIME magic-byte detection works.
func makeJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("jpeg.Encode: %v", err)
	}
	return buf.Bytes()
}

func TestBuildUploadBlock_VoiceNote(t *testing.T) {
	store := newTestStore(t)

	block, path, err := BuildUploadBlock(store, []byte("\x4f\x67\x67\x53ogg-bytes"), UploadMeta{
		Channel:        "telegram",
		ChannelUserID:  "12345",
		ChatID:         "c1",
		ProvidedMime:   "audio/ogg",
		IsVoiceNote:    true,
		Duration:       7,
		ForceMediaType: "voice",
	})
	if err != nil {
		t.Fatalf("BuildUploadBlock: %v", err)
	}
	if block.Type != "audio" {
		t.Fatalf("expected audio block, got %q", block.Type)
	}
	if block.Duration != 7 {
		t.Fatalf("expected duration 7, got %d", block.Duration)
	}
	if block.MimeType != "audio/ogg" {
		t.Fatalf("expected audio/ogg, got %q", block.MimeType)
	}
	if block.Source != "telegram" {
		t.Fatalf("expected source telegram, got %q", block.Source)
	}
	if path == "" || block.FilePath == "" {
		t.Fatalf("expected FilePath/absPath to be populated")
	}
	if !strings.Contains(path, "/uploads/telegram/") {
		t.Fatalf("expected path under uploads/telegram/, got %q", path)
	}
	if !strings.Contains(path, "/voice/") {
		t.Fatalf("expected ForceMediaType 'voice' to drive subdir, got %q", path)
	}
}

func TestBuildUploadBlock_ImageByMIME(t *testing.T) {
	store := newTestStore(t)
	data := makeJPEG(t)

	block, path, err := BuildUploadBlock(store, data, UploadMeta{
		Channel:       "whatsapp",
		ChannelUserID: "abc",
		ChatID:        "c1",
		OriginalName:  "photo.jpg",
	})
	if err != nil {
		t.Fatalf("BuildUploadBlock: %v", err)
	}
	if block.Type != "image" {
		t.Fatalf("expected image block, got %q", block.Type)
	}
	if block.Source != "whatsapp" {
		t.Fatalf("expected source whatsapp, got %q", block.Source)
	}
	if block.FileName != "" {
		t.Fatalf("image blocks should not carry FileName, got %q", block.FileName)
	}
	if !strings.HasPrefix(block.MimeType, "image/") {
		t.Fatalf("expected image/* MIME, got %q", block.MimeType)
	}
	if !strings.Contains(path, "/image/") {
		t.Fatalf("expected MIME-derived 'image' subdir, got %q", path)
	}
}

func TestBuildUploadBlock_FileDefault(t *testing.T) {
	store := newTestStore(t)

	pdfBytes := []byte("%PDF-1.4\n%\xE2\xE3\xCF\xD3\nrest-of-file")
	block, path, err := BuildUploadBlock(store, pdfBytes, UploadMeta{
		Channel:        "telegram",
		ChannelUserID:  "12345",
		ChatID:         "c1",
		OriginalName:   "report.pdf",
		ProvidedMime:   "application/pdf",
		ForceMediaType: "document",
	})
	if err != nil {
		t.Fatalf("BuildUploadBlock: %v", err)
	}
	if block.Type != "file" {
		t.Fatalf("expected file block, got %q", block.Type)
	}
	if block.FileName != "report.pdf" {
		t.Fatalf("expected FileName to propagate, got %q", block.FileName)
	}
	if block.MimeType != "application/pdf" {
		t.Fatalf("expected application/pdf, got %q", block.MimeType)
	}
	if !strings.HasSuffix(path, ".pdf") {
		t.Fatalf("expected .pdf extension from OriginalName, got %q", path)
	}
	if !strings.Contains(path, "/document/") {
		t.Fatalf("expected document subdir, got %q", path)
	}
}

func TestBuildUploadBlock_AudioFileIsFileBlock(t *testing.T) {
	store := newTestStore(t)

	// audio/* that is NOT a voice note should still land as a file block
	// per the channel-agnostic rule. Channels that want STT must pass IsVoiceNote.
	block, _, err := BuildUploadBlock(store, []byte("ID3\x03music-bytes"), UploadMeta{
		Channel:       "telegram",
		ChannelUserID: "12345",
		ChatID:        "c1",
		ProvidedMime:  "audio/mpeg",
		OriginalName:  "song.mp3",
	})
	if err != nil {
		t.Fatalf("BuildUploadBlock: %v", err)
	}
	if block.Type != "file" {
		t.Fatalf("non-voice-note audio must be a file block, got %q", block.Type)
	}
	if block.FileName != "song.mp3" {
		t.Fatalf("expected FileName propagated, got %q", block.FileName)
	}
}

func TestBuildUploadBlock_SourceOverride(t *testing.T) {
	store := newTestStore(t)
	data := makeJPEG(t)

	block, _, err := BuildUploadBlock(store, data, UploadMeta{
		Channel:       "whatsapp",
		Source:        "custom-source",
		ChannelUserID: "abc",
		ChatID:        "c1",
	})
	if err != nil {
		t.Fatalf("BuildUploadBlock: %v", err)
	}
	if block.Source != "custom-source" {
		t.Fatalf("expected Source override, got %q", block.Source)
	}
}

func TestBuildUploadBlock_MIMEFallbackUnknown(t *testing.T) {
	store := newTestStore(t)

	data := []byte{0x01, 0x02, 0x03, 0x04}
	block, path, err := BuildUploadBlock(store, data, UploadMeta{
		Channel:       "http",
		ChannelUserID: "",
		ChatID:        "c1",
	})
	if err != nil {
		t.Fatalf("BuildUploadBlock: %v", err)
	}
	if block.Type != "file" {
		t.Fatalf("expected file block for unknown bytes, got %q", block.Type)
	}
	if block.MimeType == "" {
		t.Fatalf("expected MIME to be detected or defaulted")
	}
	if !strings.HasSuffix(path, ".bin") {
		t.Fatalf("expected .bin fallback extension, got %q", path)
	}
}

func TestBuildUploadBlock_EmptyDataRejected(t *testing.T) {
	store := newTestStore(t)
	if _, _, err := BuildUploadBlock(store, nil, UploadMeta{Channel: "http"}); err == nil {
		t.Fatalf("expected error for empty data")
	}
}

func TestBuildUploadBlock_NilStore(t *testing.T) {
	if _, _, err := BuildUploadBlock(nil, []byte("x"), UploadMeta{Channel: "http"}); err == nil {
		t.Fatalf("expected error for nil store")
	}
}

func TestExtForMIMEStripsParameters(t *testing.T) {
	if got := extForMIME("audio/ogg; codecs=opus"); got != ".ogg" {
		t.Fatalf("expected .ogg, got %q", got)
	}
}
