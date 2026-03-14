// Package web provides browser-based setup wizard and configuration editor
package web

import (
	"github.com/roelfdiedericks/goclaw/internal/auth"
	"github.com/roelfdiedericks/goclaw/internal/channels/http/config"
	telegramconfig "github.com/roelfdiedericks/goclaw/internal/channels/telegram/config"
	whatsappconfig "github.com/roelfdiedericks/goclaw/internal/channels/whatsapp/config"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	"github.com/roelfdiedericks/goclaw/internal/cron"
	"github.com/roelfdiedericks/goclaw/internal/gateway"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	"github.com/roelfdiedericks/goclaw/internal/media"
	"github.com/roelfdiedericks/goclaw/internal/memorygraph"
	"github.com/roelfdiedericks/goclaw/internal/sandbox"
	"github.com/roelfdiedericks/goclaw/internal/session"
	"github.com/roelfdiedericks/goclaw/internal/skills"
	"github.com/roelfdiedericks/goclaw/internal/stt"
	"github.com/roelfdiedericks/goclaw/internal/transcript"
	"github.com/roelfdiedericks/goclaw/internal/voicellm"
)

// FormDefGetter is a function that returns a FormDef for a config section
type FormDefGetter func() forms.FormDef

// formDefRegistry maps section IDs to their FormDef getters
// Each component's config.go defines its own ConfigFormDef
var formDefRegistry = map[string]FormDefGetter{
	// Configuration
	"llm-providers": llmProvidersFormDef,
	"llm":           llm.ConfigFormDef,
	"voicellm":      voicellm.ConfigFormDef,
	"gateway":       gateway.ConfigFormDef,
	"session":       session.ConfigFormDef,

	// Channels
	"telegram": telegramconfig.ConfigFormDef,
	"http":     config.ConfigFormDef,
	"whatsapp": whatsappconfig.ConfigFormDef,

	// Services
	"transcript":  transcript.ConfigFormDef,
	"memorygraph": memorygraph.ConfigFormDef,
	"stt":         stt.ConfigFormDef,
	"skills":      skills.ConfigFormDef,
	"cron":        cron.ConfigFormDef,

	// System
	"sandbox": sandbox.ConfigFormDef,
	"auth":    auth.ConfigFormDef,
	"media":   media.ConfigFormDef,
	"roles":   rolesFormDef,
}

// GetFormDef returns the FormDef for a section, or nil if not found
func GetFormDef(sectionID string) *forms.FormDef {
	getter, ok := formDefRegistry[sectionID]
	if !ok {
		return nil
	}
	def := getter()
	return &def
}

// RegisterFormDef allows dynamic registration of FormDef getters
func RegisterFormDef(sectionID string, getter FormDefGetter) {
	formDefRegistry[sectionID] = getter
}

// llmProvidersFormDef returns the FormDef for LLM provider management
func llmProvidersFormDef() forms.FormDef {
	return forms.FormDef{
		Title:       "LLM Providers",
		Description: "Configure API connections to language model providers",
		Sections: []forms.Section{
			{
				Fields: []forms.Field{
					{
						Name: "providers",
						Type: forms.ProviderList,
					},
				},
			},
		},
	}
}

// rolesFormDef returns the FormDef for role permission management
func rolesFormDef() forms.FormDef {
	return forms.FormDef{
		Title:       "Roles",
		Description: "Configure permissions and access levels for different user roles",
		Sections: []forms.Section{
			{
				Fields: []forms.Field{
					{
						Name: "",
						Type: forms.RolesList,
					},
				},
			},
		},
	}
}
