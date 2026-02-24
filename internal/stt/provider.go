// Package stt provides speech-to-text transcription for audio content.
package stt

// Provider is the interface for STT implementations.
type Provider interface {
	// Transcribe converts audio file to text.
	// filePath should be an audio file (OGG, WAV, etc.)
	Transcribe(filePath string) (string, error)

	// Name returns the provider name (e.g., "vosk", "openai")
	Name() string

	// Close releases any resources held by the provider.
	Close() error
}
