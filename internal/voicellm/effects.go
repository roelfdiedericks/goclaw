package voicellm

// --------------------------------------------------------------------------------------------------------------------------------
// By your command. Though the stars may align against us, we fight on—for survival, for the remnant of humanity. So say we all?
// --------------------------------------------------------------------------------------------------------------------------------

import (
	"encoding/binary"
	"math"
)

// AudioEffects applies configured effects to PCM int16 audio data.
// It maintains state (sample offset) across calls for continuous processing.
type AudioEffects struct {
	config       EffectsConfig
	sampleRate   int
	sampleOffset int64
	lastSample   int16 // held sample for downsampling
}

// NewAudioEffects creates an effects processor from config
func NewAudioEffects(cfg EffectsConfig, sampleRate int) *AudioEffects {
	return &AudioEffects{
		config:     cfg,
		sampleRate: sampleRate,
	}
}

// IsEnabled returns true if any effects are configured
func (fx *AudioEffects) IsEnabled() bool {
	return fx.config.Mode != "" && fx.config.Mode != "none"
}

// Process applies configured effects to raw PCM bytes (little-endian int16).
// Modifies the buffer in-place and returns it.
func (fx *AudioEffects) Process(pcm []byte) []byte {
	if !fx.IsEnabled() || len(pcm) < 2 {
		return pcm
	}

	samples := pcmToSamples(pcm)

	switch fx.config.Mode {
	case "ring":
		fx.applyRingMod(samples)
	case "bitcrush":
		fx.applyBitcrush(samples)
	case "both":
		fx.applyRingMod(samples)
		fx.applyBitcrush(samples)
	}

	fx.sampleOffset += int64(len(samples))

	samplesToPCM(samples, pcm)
	return pcm
}

// applyRingMod multiplies each sample by a sine carrier wave
func (fx *AudioEffects) applyRingMod(samples []int16) {
	freq := fx.config.Ring.CarrierFreq
	mix := fx.config.Ring.Mix

	if freq <= 0 {
		freq = 200
	}
	if mix <= 0 || mix > 1 {
		mix = 0.7
	}

	sr := float64(fx.sampleRate)
	for i, s := range samples {
		t := float64(fx.sampleOffset+int64(i)) / sr
		carrier := math.Sin(2 * math.Pi * freq * t)

		dry := float64(s)
		wet := dry * carrier
		mixed := dry*(1-mix) + wet*mix

		samples[i] = clampInt16(mixed)
	}
}

// applyBitcrush reduces bit depth and optionally downsamples
func (fx *AudioEffects) applyBitcrush(samples []int16) {
	bitDepth := fx.config.Bitcrush.BitDepth
	downsample := fx.config.Bitcrush.Downsample

	if bitDepth <= 0 || bitDepth > 16 {
		bitDepth = 8
	}
	if downsample <= 0 {
		downsample = 1
	}

	// Bit reduction: quantize to fewer levels
	if bitDepth < 16 {
		levels := float64(int(1) << bitDepth) // 2^bitDepth
		halfLevels := levels / 2
		for i, s := range samples {
			normalized := float64(s) / 32768.0                          // [-1, 1)
			quantized := math.Round(normalized*halfLevels) / halfLevels // snap to grid
			samples[i] = clampInt16(quantized * 32768.0)
		}
	}

	// Downsample: hold every Nth sample
	if downsample > 1 {
		for i := range samples {
			if i%downsample == 0 {
				fx.lastSample = samples[i]
			} else {
				samples[i] = fx.lastSample
			}
		}
	}
}

func pcmToSamples(pcm []byte) []int16 {
	n := len(pcm) / 2
	samples := make([]int16, n)
	for i := 0; i < n; i++ {
		// G115: uint16 to int16 is intentional - PCM audio uses signed 16-bit samples
		samples[i] = int16(binary.LittleEndian.Uint16(pcm[i*2:])) //nolint:gosec
	}
	return samples
}

func samplesToPCM(samples []int16, pcm []byte) {
	for i, s := range samples {
		// G115: int16 to uint16 is intentional - converting signed PCM back to bytes
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(s)) //nolint:gosec
	}
}

func clampInt16(v float64) int16 {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}
