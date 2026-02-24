package stt

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pion/opus"
	"github.com/pion/opus/pkg/oggreader"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/zeozeozeo/gomplerate"
)

const (
	targetSampleRate = 16000 // Whisper.cpp requires 16kHz
	maxFrameSize     = 5760  // Max Opus frame size (120ms at 48kHz)
)

// ConvertToFloat32 converts an audio file to 16kHz mono float32 samples.
// This is the format required by Whisper.cpp.
// For OGG/Opus files (Telegram, WhatsApp), tries pure Go decoding first,
// then falls back to ffmpeg if pure Go fails.
func ConvertToFloat32(filePath string) ([]float32, error) {
	ext := strings.ToLower(filepath.Ext(filePath))

	// OGG/Opus: Use ffmpeg if available (pion/opus is unreliable)
	// The pure Go library has limited codec support and panics on some files
	if ext == ".ogg" || ext == ".opus" || ext == ".oga" {
		if ffmpegAvailable() {
			L_debug("stt: using ffmpeg for OGG/Opus", "file", filePath)
			return convertWithFFmpeg(filePath)
		}
		// Fallback to pure Go with panic recovery
		samples, err := convertOggOpusPureGoSafe(filePath)
		if err != nil {
			return nil, fmt.Errorf("OGG decoding failed (%v) - install ffmpeg for reliable audio conversion", err)
		}
		return samples, nil
	}

	// Other formats: ffmpeg only
	if ffmpegAvailable() {
		L_debug("stt: using ffmpeg for non-OGG format", "file", filePath, "ext", ext)
		return convertWithFFmpeg(filePath)
	}

	return nil, fmt.Errorf("unsupported audio format %s (install ffmpeg for non-OGG files)", ext)
}

// convertOggOpusPureGoSafe wraps convertOggOpusPureGo with panic recovery.
// The pion/opus library has bugs that can cause panics on some files.
func convertOggOpusPureGoSafe(filePath string) (samples []float32, err error) {
	defer func() {
		if r := recover(); r != nil {
			L_warn("stt: pure Go decoder panicked, recovered", "panic", r)
			err = fmt.Errorf("decoder panic: %v", r)
			samples = nil
		}
	}()
	return convertOggOpusPureGo(filePath)
}

// convertOggOpusPureGo decodes OGG/Opus to 16kHz mono float32 using pure Go.
func convertOggOpusPureGo(filePath string) ([]float32, error) {
	L_debug("stt: decoding OGG/Opus", "file", filePath)

	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open audio file: %w", err)
	}
	defer file.Close()

	// Parse OGG container
	ogg, header, err := oggreader.NewWith(file)
	if err != nil {
		return nil, fmt.Errorf("parse OGG container: %w", err)
	}

	sampleRate := int(header.SampleRate)
	channels := int(header.Channels)
	L_debug("stt: OGG header", "sampleRate", sampleRate, "channels", channels)

	// Create Opus decoder
	decoder := opus.NewDecoder()

	// Decode all pages
	// Output buffer: Opus can output up to maxFrameSize samples per channel
	outBuf := make([]byte, maxFrameSize*channels*2) // *2 for 16-bit samples

	var allSamples []int16
	for {
		segments, _, err := ogg.ParseNextPage()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse OGG page: %w", err)
		}

		// Each segment is an Opus packet
		for _, segment := range segments {
			if len(segment) == 0 {
				continue
			}

			// Decode Opus packet to PCM bytes
			_, isStereo, err := decoder.Decode(segment, outBuf)
			if err != nil {
				L_trace("stt: skipping packet", "error", err, "len", len(segment))
				continue
			}

			// Determine actual output size based on frame
			// pion/opus Decode fills outBuf with 16-bit LE PCM
			// We need to figure out how many samples were decoded
			actualChannels := 1
			if isStereo {
				actualChannels = 2
			}

			// Parse int16 samples from output buffer
			// The number of samples depends on the frame duration
			// We'll read until we hit zeros or end of buffer
			samples := bytesToInt16(outBuf, actualChannels)
			allSamples = append(allSamples, samples...)
		}
	}

	if len(allSamples) == 0 {
		return nil, fmt.Errorf("no audio samples decoded from %s", filePath)
	}

	L_debug("stt: decoded samples", "count", len(allSamples), "sampleRate", sampleRate)

	// Convert to mono if stereo
	if channels > 1 {
		allSamples = toMono(allSamples, channels)
	}

	// Resample to 16kHz if needed
	if sampleRate != targetSampleRate {
		L_debug("stt: resampling", "from", sampleRate, "to", targetSampleRate)
		allSamples = resampleInt16(allSamples, sampleRate, targetSampleRate)
	}

	// Convert int16 to float32 (normalized to [-1, 1])
	result := int16ToFloat32(allSamples)

	L_debug("stt: conversion complete", "samples", len(result), "duration_sec", float64(len(result))/float64(targetSampleRate))

	return result, nil
}

// bytesToInt16 converts a byte buffer to int16 samples (little-endian).
func bytesToInt16(buf []byte, channels int) []int16 {
	numSamples := len(buf) / 2
	samples := make([]int16, 0, numSamples)

	for i := 0; i < len(buf)-1; i += 2 {
		sample := int16(binary.LittleEndian.Uint16(buf[i : i+2])) // #nosec G115 - safe: uint16 to int16 for audio samples
		// Stop at trailing zeros (unused buffer space)
		if sample == 0 && i > 0 {
			// Check if remaining buffer is all zeros
			allZero := true
			for j := i; j < len(buf)-1; j += 2 {
				if binary.LittleEndian.Uint16(buf[j:j+2]) != 0 {
					allZero = false
					break
				}
			}
			if allZero {
				break
			}
		}
		samples = append(samples, sample)
	}

	return samples
}

// toMono converts multi-channel audio to mono by averaging channels.
func toMono(samples []int16, channels int) []int16 {
	if channels == 1 {
		return samples
	}

	mono := make([]int16, len(samples)/channels)
	for i := 0; i < len(mono); i++ {
		var sum int32
		for ch := 0; ch < channels; ch++ {
			sum += int32(samples[i*channels+ch])
		}
		mono[i] = int16(sum / int32(channels)) // #nosec G115 - safe: channels is small (1-8)
	}
	return mono
}

// resampleInt16 converts audio from one sample rate to another using gomplerate.
func resampleInt16(samples []int16, fromRate, toRate int) []int16 {
	if fromRate == toRate {
		return samples
	}

	// Create resampler (mono)
	resampler, err := gomplerate.NewResampler(1, fromRate, toRate)
	if err != nil {
		L_warn("stt: resampler creation failed, skipping resample", "error", err)
		return samples
	}

	// Resample directly with int16
	return resampler.ResampleInt16(samples)
}

// int16ToFloat32 converts int16 samples to float32 normalized to [-1, 1].
func int16ToFloat32(samples []int16) []float32 {
	result := make([]float32, len(samples))
	for i, s := range samples {
		result[i] = float32(s) / 32768.0
	}
	return result
}

// ffmpegAvailable checks if ffmpeg is installed.
func ffmpegAvailable() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

// convertWithFFmpeg uses ffmpeg to convert audio to 16kHz mono PCM.
func convertWithFFmpeg(inputPath string) ([]float32, error) {
	// Create temp file for raw PCM output
	tmpFile, err := os.CreateTemp("", "stt-*.raw")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	// Convert to raw 16-bit PCM
	// #nosec G204 - inputPath is from internal file operations, not user input
	cmd := exec.Command("ffmpeg",
		"-i", inputPath,
		"-ar", fmt.Sprintf("%d", targetSampleRate),
		"-ac", "1",
		"-f", "s16le",
		"-acodec", "pcm_s16le",
		"-y",
		tmpPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		L_debug("stt: ffmpeg output", "output", string(output))
		return nil, fmt.Errorf("ffmpeg conversion failed: %w", err)
	}

	// Read raw PCM data
	rawData, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("read converted audio: %w", err)
	}

	// Convert bytes to int16
	samples := make([]int16, len(rawData)/2)
	for i := 0; i < len(samples); i++ {
		samples[i] = int16(rawData[i*2]) | int16(rawData[i*2+1])<<8
	}

	// Convert to float32
	return int16ToFloat32(samples), nil
}
