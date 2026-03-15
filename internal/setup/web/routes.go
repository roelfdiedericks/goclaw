package web

import (
	"io/fs"
	"net/http"

	"github.com/roelfdiedericks/goclaw/internal/configapply"
)

type mountOptions struct {
	configPath     string
	wrap           func(http.HandlerFunc) http.HandlerFunc
	handlers       *Handlers
	applyCaller    configapply.Caller
	enableShutdown bool
	shutdown       http.HandlerFunc
}

func mountSetup(mux *http.ServeMux, opts mountOptions) {
	wrap := opts.wrap
	if wrap == nil {
		wrap = func(h http.HandlerFunc) http.HandlerFunc { return h }
	}

	if opts.handlers != nil {
		mux.HandleFunc("/setup/wizard", wrap(opts.handlers.HandleWizard))
		mux.HandleFunc("/setup/edit", wrap(opts.handlers.HandleEdit))
	}

	staticSub, err := fs.Sub(staticFS, "static")
	if err == nil {
		mux.Handle("/setup/static/", http.StripPrefix("/setup/static/", http.FileServer(http.FS(staticSub))))
	}

	api := NewAPI(opts.configPath, opts.applyCaller)
	usersAPI := NewUsersAPI(opts.configPath)
	wizardAPI := NewWizardAPI(opts.configPath, opts.applyCaller)

	mux.HandleFunc("/setup/api/config", wrap(api.HandleGetConfig))
	mux.HandleFunc("/setup/api/sections", wrap(api.HandleGetSections))
	mux.HandleFunc("/setup/api/section/", wrap(api.HandleSection))
	mux.HandleFunc("/setup/api/apply", wrap(api.HandleApply))
	mux.HandleFunc("/setup/api/providers", wrap(api.HandleGetProviders))
	mux.HandleFunc("/setup/api/models/", wrap(api.HandleGetModels))
	mux.HandleFunc("/setup/api/presets", wrap(api.HandleGetPresets))
	mux.HandleFunc("/setup/api/drivers", wrap(api.HandleGetDrivers))

	mux.HandleFunc("/setup/api/users", wrap(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			usersAPI.HandleListUsers(w, r)
		case http.MethodPost:
			usersAPI.HandleCreateUser(w, r)
		default:
			writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		}
	}))
	mux.HandleFunc("/setup/api/users/", wrap(usersAPI.HandleUser))
	mux.HandleFunc("/setup/api/roles", wrap(usersAPI.HandleRoles))

	mux.HandleFunc("/setup/api/wizard/state", wrap(wizardAPI.HandleGetState))
	mux.HandleFunc("/setup/api/wizard/step", wrap(wizardAPI.HandleGetStep))
	mux.HandleFunc("/setup/api/wizard/submit", wrap(wizardAPI.HandleSubmitStep))
	mux.HandleFunc("/setup/api/wizard/prev", wrap(wizardAPI.HandlePrevStep))
	mux.HandleFunc("/setup/api/wizard/finish", wrap(wizardAPI.HandleFinish))
	mux.HandleFunc("/setup/api/wizard/models/", wrap(wizardAPI.HandleGetModels))

	if opts.enableShutdown && opts.shutdown != nil {
		mux.HandleFunc("/setup/api/shutdown", opts.shutdown)
	}
}
