// Package web provides browser-based setup wizard and configuration editor
package web

import (
	"embed"
	"html/template"
	"net/http"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Handlers provides HTTP handlers for the setup wizard and editor
type Handlers struct {
	wizardTmpl *template.Template
	editTmpl   *template.Template
	setupMode  bool // true for standalone wizard (no auth, minimal nav)
}

// NewHandlers creates new setup handlers
func NewHandlers(setupMode bool) (*Handlers, error) {
	// Parse templates separately to avoid namespace collisions
	// (both define "content" block, last one wins if parsed together)
	wizardTmpl, err := template.ParseFS(templatesFS, "templates/base.html", "templates/wizard.html")
	if err != nil {
		return nil, err
	}
	editTmpl, err := template.ParseFS(templatesFS, "templates/base.html", "templates/edit.html")
	if err != nil {
		return nil, err
	}
	return &Handlers{
		wizardTmpl: wizardTmpl,
		editTmpl:   editTmpl,
		setupMode:  setupMode,
	}, nil
}

// TemplateData holds common data for all templates
type TemplateData struct {
	StandaloneMode bool       // true for standalone setup process
	WizardMode     bool       // true when rendering wizard page
	EditMode       bool       // true when rendering setup editor page
	User           *user.User // authenticated user (nil in standalone mode)
	Title          string
	Categories     []SectionCategory // sidebar categories
	Content        template.HTML     // main content
	CurrentNav     string            // active nav item
}

// HandleWizard serves the wizard page
func (h *Handlers) HandleWizard(w http.ResponseWriter, r *http.Request) {
	data := TemplateData{
		StandaloneMode: h.setupMode,
		WizardMode:     true,
		EditMode:       false,
		Title:          "Setup Wizard",
		CurrentNav:     "wizard",
	}

	if err := h.wizardTmpl.ExecuteTemplate(w, "base", data); err != nil {
		L_error("web: template error", "template", "wizard.html", "error", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

// HandleEdit serves the configuration editor page
func (h *Handlers) HandleEdit(w http.ResponseWriter, r *http.Request) {
	data := TemplateData{
		StandaloneMode: h.setupMode,
		WizardMode:     false,
		EditMode:       true,
		Title:          "Configuration Editor",
		Categories:     EditorSections,
		CurrentNav:     "edit",
	}

	if err := h.editTmpl.ExecuteTemplate(w, "base", data); err != nil {
		L_error("web: template error", "template", "edit.html", "error", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

// RegisterRoutes is kept for compatibility with older call sites.
// New code should prefer mountSetup for pages, APIs, and static assets together.
func (h *Handlers) RegisterRoutes(mux *http.ServeMux, configPath string) {
	mountSetup(mux, mountOptions{
		configPath: configPath,
		handlers:   h,
	})
}
