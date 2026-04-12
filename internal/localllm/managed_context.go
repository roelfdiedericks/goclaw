package localllm

import . "github.com/roelfdiedericks/goclaw/internal/logging"

// deriveManagedEffectiveContext picks managed llama-server context size:
// explicit override (ManagedSpec.ContextSize > 0), else GGUF metadata, else catalog fallback.
func deriveManagedEffectiveContext(override int, modelPath string, catalogFallback int) int {
	if override > 0 {
		return override
	}
	n, found, err := ReadGGUFContextLength(modelPath)
	if err != nil {
		L_warn("localllm: could not read GGUF context_length", "path", modelPath, "error", err)
		return catalogFallback
	}
	if found && n > 0 {
		return n
	}
	return catalogFallback
}
