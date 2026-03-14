package forms

import (
	"strings"
	"testing"

	"github.com/rivo/tview"
)

func TestBuildFormContentStrictContractViolation(t *testing.T) {
	type Cfg struct {
		Gateway struct {
			LogFile string `json:"logFile"`
		} `json:"gateway"`
	}

	cfg := &Cfg{}
	def := FormDef{
		Title: "bad",
		Sections: []Section{
			{
				Fields: []Field{
					{Name: "Gateway.LogFile", Type: Text},
				},
			},
		},
	}

	_, err := BuildFormContent(def, cfg, "test", nil, tview.NewApplication())
	if err == nil {
		t.Fatalf("expected strict contract violation")
	}
	if !strings.Contains(err.Error(), "strict form contract violation") {
		t.Fatalf("unexpected error: %v", err)
	}
}
