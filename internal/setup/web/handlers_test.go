package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleWizardRendersStatusNoteHooks(t *testing.T) {
	handlers, err := NewHandlers(true, false)
	if err != nil {
		t.Fatalf("new handlers: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/setup/wizard", nil)
	rec := httptest.NewRecorder()
	handlers.HandleWizard(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	for _, token := range []string{
		`id="wizard-step-status-note"`,
		`id="wizard-step-status-note-body"`,
		`id="wizard-step-status-note-icon"`,
		`id="wizard-step-status-note-text"`,
		`/setup/static/editor.js?v=`,
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("expected wizard output to contain %q", token)
		}
	}
}

func TestWizardStaticJSIncludesBlockedStepFeedback(t *testing.T) {
	mux := http.NewServeMux()
	mountSetup(mux, mountOptions{configPath: filepath.Join(t.TempDir(), "goclaw.json")})

	req := httptest.NewRequest(http.MethodGet, "/setup/static/editor.js", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected static asset 200, got %d", rec.Code)
	}

	bodyBytes, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	body := string(bodyBytes)

	for _, token := range []string{
		"getCurrentStepBlocker()",
		"focusBlockedStep(blocker)",
		"ensureWizardInteractionStyles()",
		"wizard-soft-disabled",
		"wizard-blocked-shake",
		"wizard-blocked-flash",
		"findBlockedAction(blocker, $target)",
		"wizard-blocked-action-focus",
		"wizard-blocked-action-pulse",
		"closest('.js-field')",
		"console.debug('[setup wizard] next clicked'",
		"console.debug('[setup wizard] nextStep blocker check'",
		"console.debug('[setup wizard] applied wizard-blocked-focus class'",
		"You must acknowledge the permissive security warning before continuing.",
		"Complete Telegram pairing before continuing.",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("expected editor.js to contain %q", token)
		}
	}
}
