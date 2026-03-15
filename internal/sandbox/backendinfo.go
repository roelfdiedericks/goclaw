package sandbox

import (
	"runtime"

	"github.com/roelfdiedericks/goclaw/internal/config/forms"
)

const (
	BackendBubblewrap = "bubblewrap"
	BackendSeatbelt   = "seatbelt"
	BackendNone       = "none"
)

// CurrentSandboxBackend returns the managed sandbox backend for this platform.
func CurrentSandboxBackend() string {
	switch runtime.GOOS {
	case "linux":
		return BackendBubblewrap
	case "darwin":
		return BackendSeatbelt
	default:
		return BackendNone
	}
}

// CurrentBackendDisplayName returns the user-facing backend label.
func CurrentBackendDisplayName() string {
	switch CurrentSandboxBackend() {
	case BackendBubblewrap:
		return "Bubblewrap"
	case BackendSeatbelt:
		return "Seatbelt"
	default:
		return "Sandbox Backend"
	}
}

// SupportedModeOptions returns platform-appropriate sandbox modes for UI.
func SupportedModeOptions() []forms.Option {
	switch CurrentSandboxBackend() {
	case BackendBubblewrap:
		return []forms.Option{
			{Label: "Home (full isolated home - recommended)", Value: ModeHome},
			{Label: "Autodocs Read (sandbox home + non-hidden home directories)", Value: ModeAutoDocsRead},
			{Label: "Autodocs Write (sandbox home + writable non-hidden home directories)", Value: ModeAutoDocsWrite},
			{Label: "Volumes (specific dirs only)", Value: ModeVolumes},
			{Label: "Ephemeral (nothing persists)", Value: ModeEphemeral},
		}
	case BackendSeatbelt:
		return []forms.Option{
			{Label: "Home (full isolated home - recommended)", Value: ModeHome},
			{Label: "Autodocs Read (sandbox home + non-hidden home directories)", Value: ModeAutoDocsRead},
			{Label: "Autodocs Write (sandbox home + writable non-hidden home directories)", Value: ModeAutoDocsWrite},
		}
	default:
		return []forms.Option{
			{Label: "Home", Value: ModeHome},
		}
	}
}

// BackendPathFieldName returns the form field path for the current backend binary path.
func BackendPathFieldName() string {
	switch CurrentSandboxBackend() {
	case BackendSeatbelt:
		return "seatbelt.path"
	default:
		return "bubblewrap.path"
	}
}

// BackendPathDescription returns a backend-appropriate binary path description.
func BackendPathDescription() string {
	switch CurrentSandboxBackend() {
	case BackendSeatbelt:
		return "Custom path to sandbox-exec (empty = search PATH)"
	case BackendBubblewrap:
		return "Custom path to bwrap binary (empty = search PATH)"
	default:
		return "Custom path to sandbox backend binary"
	}
}
