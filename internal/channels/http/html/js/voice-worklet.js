// AudioWorklet processor for capturing microphone audio and converting to base64 PCM
// Sends at native sample rate (no resampling) - xAI API accepts various rates

class VoiceProcessor extends AudioWorkletProcessor {
    constructor() {
        super();
        // Use native sample rate - xAI supports 8k, 16k, 24k, 32k, 44.1k, 48kHz
        this.nativeSampleRate = sampleRate; // AudioWorklet's sampleRate global
        
        // Buffer ~100ms of audio at native rate (matching xAI cookbook)
        this.chunkDurationMs = 100;
        this.bufferSize = Math.floor((this.nativeSampleRate * this.chunkDurationMs) / 1000);
        this.buffer = new Float32Array(this.bufferSize);
        this.bufferIndex = 0;
        
        // Report sample rate to main thread
        this.port.postMessage({ type: 'init', sampleRate: this.nativeSampleRate, bufferSize: this.bufferSize });
    }

    process(inputs, outputs, parameters) {
        const input = inputs[0];
        if (!input || input.length === 0) return true;

        const channelData = input[0];
        if (!channelData) return true;

        // Calculate RMS for level metering
        let sum = 0;
        for (let i = 0; i < channelData.length; i++) {
            sum += channelData[i] * channelData[i];
        }
        const rms = Math.sqrt(sum / channelData.length);
        
        // Send level to main thread for visualizer (throttled)
        if (Math.random() < 0.1) { // ~10% of frames
            this.port.postMessage({ type: 'level', rms: rms });
        }

        // Accumulate samples (no resampling)
        for (let i = 0; i < channelData.length; i++) {
            this.buffer[this.bufferIndex++] = channelData[i];
            
            if (this.bufferIndex >= this.bufferSize) {
                this.sendBuffer();
            }
        }

        return true;
    }

    sendBuffer() {
        // Convert float32 to int16 PCM (little-endian)
        const int16 = new Int16Array(this.bufferIndex);
        for (let j = 0; j < this.bufferIndex; j++) {
            const s = Math.max(-1, Math.min(1, this.buffer[j]));
            int16[j] = s < 0 ? s * 0x8000 : s * 0x7FFF;
        }

        // Convert to base64
        const bytes = new Uint8Array(int16.buffer);
        const base64 = this.bytesToBase64(bytes);

        // Send to main thread
        this.port.postMessage({ type: 'audio', audio: base64 });

        // Reset buffer
        this.bufferIndex = 0;
    }

    bytesToBase64(bytes) {
        const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
        let result = '';
        const len = bytes.length;
        
        for (let i = 0; i < len; i += 3) {
            const b1 = bytes[i];
            const b2 = i + 1 < len ? bytes[i + 1] : 0;
            const b3 = i + 2 < len ? bytes[i + 2] : 0;
            
            result += chars[b1 >> 2];
            result += chars[((b1 & 3) << 4) | (b2 >> 4)];
            result += i + 1 < len ? chars[((b2 & 15) << 2) | (b3 >> 6)] : '=';
            result += i + 2 < len ? chars[b3 & 63] : '=';
        }
        
        return result;
    }
}

registerProcessor('voice-processor', VoiceProcessor);
