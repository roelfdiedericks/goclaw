package setup

import "github.com/roelfdiedericks/goclaw/internal/sandbox"

const (
	SandboxPresetAssistant  = "assistant"
	SandboxPresetPermissive = "permissive"
	SandboxPresetHardened   = "hardened"
)

type SandboxPresetWarning struct {
	Title   string
	Body    string
	Consent string
}

func SandboxPresetValues() []string {
	return []string{SandboxPresetAssistant, SandboxPresetPermissive, SandboxPresetHardened}
}

func NormalizeSandboxPreset(preset string) string {
	switch preset {
	case SandboxPresetPermissive, SandboxPresetHardened:
		return preset
	default:
		return SandboxPresetAssistant
	}
}

func ApplySandboxPreset(data *WizardData, preset string) {
	switch NormalizeSandboxPreset(preset) {
	case SandboxPresetPermissive:
		data.SandboxEnabled = false
		data.ExecSandboxEnabled = false
		data.BrowserSandboxEnabled = false
		data.FileToolsSandboxEnabled = false
		data.SandboxMode = sandbox.ModeHome
	case SandboxPresetHardened:
		data.SandboxEnabled = true
		data.ExecSandboxEnabled = false
		data.BrowserSandboxEnabled = false
		data.FileToolsSandboxEnabled = true
		data.SandboxMode = sandbox.ModeHome
	default:
		data.SandboxEnabled = true
		data.ExecSandboxEnabled = true
		data.BrowserSandboxEnabled = true
		data.FileToolsSandboxEnabled = true
		data.SandboxMode = sandbox.ModeAutoDocsWrite
	}
	data.SandboxPreset = NormalizeSandboxPreset(preset)
}

func DetectSandboxPreset(enabled bool, mode string, execEnabled bool, browserEnabled bool, fileToolsEnabled bool) (preset string, advanced bool) {
	switch {
	case !enabled:
		return SandboxPresetPermissive, false
	case enabled && mode == sandbox.ModeHome && !execEnabled && !browserEnabled && fileToolsEnabled:
		return SandboxPresetHardened, false
	case enabled && mode == sandbox.ModeAutoDocsWrite && execEnabled && browserEnabled && fileToolsEnabled:
		return SandboxPresetAssistant, false
	default:
		return SandboxPresetAssistant, true
	}
}

func SandboxPresetWarningText(preset string) SandboxPresetWarning {
	switch NormalizeSandboxPreset(preset) {
	case SandboxPresetPermissive:
		return SandboxPresetWarning{
			Title:   "Permissive mode: high risk",
			Body:    "This disables sandbox protections. The agent can access files as the GoClaw process user. Only use this if you fully trust prompts, skills, and operators.",
			Consent: "I understand Permissive mode disables sandbox protections and increases risk.",
		}
	case SandboxPresetHardened:
		return SandboxPresetWarning{
			Title:   "Hardened mode: reduced capability",
			Body:    "Hardened mode prioritizes safety for shared or multi-user workflows. High-impact tools are restricted by default, so some tasks may not work until explicitly enabled.",
			Consent: "I understand Hardened mode reduces capability in exchange for stronger default safety.",
		}
	default:
		return SandboxPresetWarning{
			Title:   "Assistant mode: review access scope",
			Body:    "Assistant mode keeps sandboxing enabled and is recommended for normal use. The agent can work with common non-hidden home folders such as Desktop, Documents, and Pictures.",
			Consent: "I understand Assistant mode allows access to common non-hidden home folders.",
		}
	}
}
