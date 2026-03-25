package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleStaticJSServesEmbeddedAudioAsset(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/js/command.mp3", nil)
	rec := httptest.NewRecorder()

	s.handleStaticJS(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "audio/mpeg") {
		t.Fatalf("expected audio/mpeg content type, got %q", ct)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("expected embedded audio asset body")
	}
}
