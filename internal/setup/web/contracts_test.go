package web

import (
	"strings"
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/config/forms"
)

func TestValidateAllSectionContractsStrictRejectsInvalidFieldPath(t *testing.T) {
	orig := formDefRegistry["gateway"]
	defer func() { formDefRegistry["gateway"] = orig }()

	formDefRegistry["gateway"] = func() forms.FormDef {
		return forms.FormDef{
			Sections: []forms.Section{
				{
					Fields: []forms.Field{
						{Name: "Gateway.LogFile", Type: forms.Text},
					},
				},
			},
		}
	}

	err := ValidateAllSectionContractsStrict()
	if err == nil {
		t.Fatalf("expected strict contract validation error")
	}
	if !strings.Contains(err.Error(), "expected JSON-key dotted paths") {
		t.Fatalf("expected json-key guidance in error, got: %v", err)
	}
}

func TestNewServerFailsOnStrictContractViolation(t *testing.T) {
	orig := formDefRegistry["gateway"]
	defer func() { formDefRegistry["gateway"] = orig }()

	formDefRegistry["gateway"] = func() forms.FormDef {
		return forms.FormDef{
			Sections: []forms.Section{
				{
					Fields: []forms.Field{
						{Name: "Gateway.LogFile", Type: forms.Text},
					},
				},
			},
		}
	}

	_, err := NewServer("")
	if err == nil {
		t.Fatalf("expected NewServer to fail on strict contract violation")
	}
}
