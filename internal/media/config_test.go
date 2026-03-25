package media

import (
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/config/forms"
)

func TestConfigFormDefUsesGBSlidersAndMBFileSize(t *testing.T) {
	def := ConfigFormDefWithValues(MediaConfig{})

	var (
		foundGlobalSlider  bool
		foundUploadsSlider bool
		foundMaxSize       bool
	)

	for _, section := range def.Sections {
		if section.Title == "Storage Limits And Location" {
			for _, field := range section.Fields {
				if field.Name == "quotas.global" {
					foundGlobalSlider = true
					if field.Type != forms.Slider || field.Unit != "GB" || field.Step != 0.5 {
						t.Fatalf("expected global quota slider in GB with 0.5 steps, got %+v", field)
					}
					if field.Default != DefaultGlobalQuotaBytes || field.Max != 100 {
						t.Fatalf("expected 50 GB default and 100 GB max, got default=%v max=%v", field.Default, field.Max)
					}
				}
				if field.Name == "maxSize" {
					foundMaxSize = true
					if field.Type != forms.Number || field.Unit != "MB" || field.Scale != bytesPerMB {
						t.Fatalf("expected maxSize in MB number field, got %+v", field)
					}
				}
			}
		}
		if section.Title == "Uploads Directory" && section.Nested != nil {
			for _, nested := range section.Nested.Sections {
				for _, field := range nested.Fields {
					if field.Name == "uploads" {
						foundUploadsSlider = true
						if field.Type != forms.Slider || field.Unit != "GB" || field.Step != 0.5 {
							t.Fatalf("expected uploads quota slider in GB with 0.5 steps, got %+v", field)
						}
					}
				}
			}
		}
	}

	if !foundGlobalSlider {
		t.Fatal("global quota slider not found")
	}
	if !foundUploadsSlider {
		t.Fatal("uploads quota slider not found")
	}
	if !foundMaxSize {
		t.Fatal("maxSize field not found")
	}
}
