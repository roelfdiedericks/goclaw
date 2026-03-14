package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestMountSetupWrapsHandlers(t *testing.T) {
	mux := http.NewServeMux()
	wrapCalls := 0
	configPath := filepath.Join(t.TempDir(), "goclaw.json")

	mountSetup(mux, mountOptions{
		configPath: configPath,
		wrap: func(h http.HandlerFunc) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				wrapCalls++
				w.Header().Set("X-Wrapped", "yes")
				h(w, r)
			}
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/setup/api/config", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Header().Get("X-Wrapped") != "yes" {
		t.Fatalf("expected wrapped header to be set")
	}
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected method not allowed, got %d", rec.Code)
	}
	if wrapCalls == 0 {
		t.Fatalf("expected wrapper to be invoked")
	}
}

func TestMountSetupUsersAndWizardEndpoints(t *testing.T) {
	mux := http.NewServeMux()
	configPath := filepath.Join(t.TempDir(), "goclaw.json")
	mountSetup(mux, mountOptions{configPath: configPath})

	usersReq := httptest.NewRequest(http.MethodGet, "/setup/api/users", nil)
	usersRec := httptest.NewRecorder()
	mux.ServeHTTP(usersRec, usersReq)
	if usersRec.Code != http.StatusOK {
		t.Fatalf("expected users endpoint 200, got %d", usersRec.Code)
	}

	wizardReq := httptest.NewRequest(http.MethodGet, "/setup/api/wizard/state", nil)
	wizardRec := httptest.NewRecorder()
	mux.ServeHTTP(wizardRec, wizardReq)
	if wizardRec.Code != http.StatusOK {
		t.Fatalf("expected wizard state endpoint 200, got %d", wizardRec.Code)
	}
}

func TestMountSetupServesStaticAssets(t *testing.T) {
	mux := http.NewServeMux()
	mountSetup(mux, mountOptions{configPath: filepath.Join(t.TempDir(), "goclaw.json")})

	req := httptest.NewRequest(http.MethodGet, "/setup/static/editor.js", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected setup static asset 200, got %d", rec.Code)
	}
}
