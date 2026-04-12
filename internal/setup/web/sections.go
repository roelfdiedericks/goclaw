// Package web provides browser-based setup wizard and configuration editor
package web

// SectionType indicates how a section should be rendered
type SectionType string

const (
	// SectionTypeFormDef renders section using FormDef to HTML conversion
	SectionTypeFormDef SectionType = "formdef"
	// SectionTypeCustom uses custom UI (e.g., Users CRUD)
	SectionTypeCustom SectionType = "custom"
)

// SectionItem represents a single menu item in the editor sidebar
type SectionItem struct {
	ID         string      `json:"id"`         // Unique identifier
	Label      string      `json:"label"`      // Display name
	ConfigPath string      `json:"configPath"` // JSON Pointer in config (e.g., "/channels/telegram")
	Type       SectionType `json:"type"`       // formdef or custom
	Expandable bool        `json:"expandable"` // Has nested items (LLM providers)
}

// SectionCategory groups related section items
type SectionCategory struct {
	Title string        `json:"title"`
	Items []SectionItem `json:"items"`
}

// EditorSections defines the full sidebar structure for the configuration editor.
// Some sections may be filtered out at runtime based on mode/capabilities.
var EditorSections = []SectionCategory{
	{
		Title: "Configuration",
		Items: []SectionItem{
			{ID: "llm", Label: "Model Chains", ConfigPath: "/llm", Type: SectionTypeFormDef, Expandable: true},
			{ID: "llm-providers", Label: "LLM Providers", ConfigPath: "/llm", Type: SectionTypeFormDef},
			{ID: "local-llm", Label: "Local LLM", ConfigPath: "/llm", Type: SectionTypeCustom},
			{ID: "voicellm", Label: "VoiceLLM", ConfigPath: "/voicellm", Type: SectionTypeFormDef},
			{ID: "acp", Label: "Coding Agents", ConfigPath: "/acp", Type: SectionTypeFormDef},
			{ID: "a2a", Label: "A2A Networking", ConfigPath: "/a2a", Type: SectionTypeFormDef},
			{ID: "gateway", Label: "Gateway", ConfigPath: "/", Type: SectionTypeFormDef},
			{ID: "session", Label: "Session", ConfigPath: "/session", Type: SectionTypeFormDef},
		},
	},
	{
		Title: "Channels",
		Items: []SectionItem{
			{ID: "telegram", Label: "Telegram", ConfigPath: "/channels/telegram", Type: SectionTypeFormDef},
			{ID: "http", Label: "HTTP Server", ConfigPath: "/channels/http", Type: SectionTypeFormDef},
			{ID: "whatsapp", Label: "WhatsApp", ConfigPath: "/channels/whatsapp", Type: SectionTypeFormDef},
		},
	},
	{
		Title: "Services",
		Items: []SectionItem{
			{ID: "transcript", Label: "Transcript", ConfigPath: "/transcript", Type: SectionTypeFormDef},
			{ID: "memorygraph", Label: "Memory Graph", ConfigPath: "/memoryGraph", Type: SectionTypeFormDef},
			{ID: "stt", Label: "Speech-to-Text", ConfigPath: "/stt", Type: SectionTypeFormDef},
			{ID: "skills", Label: "Skills", ConfigPath: "/skills", Type: SectionTypeFormDef},
			{ID: "cron", Label: "Cron", ConfigPath: "/cron", Type: SectionTypeFormDef},
			{ID: "tools", Label: "Tools", ConfigPath: "/tools", Type: SectionTypeFormDef},
		},
	},
	{
		Title: "System",
		Items: []SectionItem{
			{ID: "sandbox", Label: "Sandbox", ConfigPath: "/sandbox", Type: SectionTypeFormDef},
			{ID: "auth", Label: "Auth", ConfigPath: "/auth", Type: SectionTypeFormDef},
			{ID: "a2a-peers", Label: "A2A Peers", ConfigPath: "/a2a", Type: SectionTypeCustom},
			{ID: "users", Label: "Users", ConfigPath: "/", Type: SectionTypeCustom},
			{ID: "roles", Label: "Roles", ConfigPath: "/roles", Type: SectionTypeFormDef},
			{ID: "media", Label: "Media", ConfigPath: "/media", Type: SectionTypeFormDef},
		},
	},
}

func EditorSectionsForMode(enableA2APeers bool) []SectionCategory {
	categories := make([]SectionCategory, 0, len(EditorSections))
	for _, cat := range EditorSections {
		items := make([]SectionItem, 0, len(cat.Items))
		for _, item := range cat.Items {
			if item.ID == "a2a-peers" && !enableA2APeers {
				continue
			}
			items = append(items, item)
		}
		if len(items) == 0 {
			continue
		}
		categories = append(categories, SectionCategory{
			Title: cat.Title,
			Items: items,
		})
	}
	return categories
}

// FindSection looks up a section by ID across all categories.
func FindSection(categories []SectionCategory, id string) *SectionItem {
	for _, cat := range categories {
		for i := range cat.Items {
			if cat.Items[i].ID == id {
				return &cat.Items[i]
			}
		}
	}
	return nil
}

// GetSectionsJSON returns the sections structure for JavaScript consumption
func GetSectionsJSON(enableA2APeers bool) []SectionCategory {
	return EditorSectionsForMode(enableA2APeers)
}
