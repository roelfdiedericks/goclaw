package acp

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

const configPath = "acp"

type Config struct {
	DefaultDriver string        `json:"defaultDriver" default:"cursor"`
	Drivers       DriversConfig `json:"drivers"`
}

type DriversConfig struct {
	Cursor CursorConfig `json:"cursor"`
}

type CursorConfig struct {
	Model string `json:"model" default:"claude-4.6-opus-high-thinking"`
}

type RefreshModelsRequest struct {
	Driver string `json:"driver"`
}

type RefreshModelsResult struct {
	Driver string           `json:"driver"`
	Count  int              `json:"count"`
	Models []ACPModelOption `json:"models"`
}

func ConfigFormDef() forms.FormDef {
	return forms.FormDef{
		Title:       "ACP",
		Description: "Configure Agent Client Protocol drivers and defaults",
		Sections: []forms.Section{
			{
				Title: "General",
				Fields: []forms.Field{
					{
						Name:    "defaultDriver",
						Title:   "Default Driver",
						Type:    forms.Select,
						Default: DriverCursor,
						Desc:    "Driver to use when ACP attach does not specify one explicitly",
						Options: []forms.Option{
							{Label: "Cursor", Value: DriverCursor},
						},
					},
				},
			},
			{
				Title: "ACP - Cursor Driver",
				Desc:  "Choose the preferred friendly model alias applied after attaching to a Cursor ACP session",
				Fields: []forms.Field{
					{
						Name:        "drivers.cursor.model",
						Title:       "Preferred Model",
						Type:        forms.SelectWithCustom,
						Default:     DefaultCursorModel,
						Desc:        "Pick a known Cursor model alias or enter a custom alias. The saved config still stores only the final model string.",
						Options:     cursorModelFieldOptions(),
						Placeholder: "Enter a custom Cursor model alias",
					},
				},
			},
		},
		Actions: []forms.ActionDef{
			{
				Name:            "refreshModelsCursor",
				Command:         "refreshModels",
				Label:           "Refresh Cursor Models",
				Desc:            "Re-query Cursor ACP and rebuild the in-memory model catalog for this process",
				Payload:         RefreshModelsRequest{Driver: DriverCursor},
				ReloadOnSuccess: true,
			},
			{Name: "apply", Label: "Apply"},
		},
	}
}

func cursorModelFieldOptions() []forms.Option {
	driver := NewCursorDriver()
	provider, ok := any(driver).(ModelCatalogProvider)
	if !ok {
		return nil
	}
	friendly := buildFriendlyModelOptions(provider.EffectiveModelCatalog())
	if len(friendly) == 0 {
		return nil
	}
	out := make([]forms.Option, 0, len(friendly))
	for _, option := range friendly {
		label := strings.TrimSpace(option.Name)
		if label == "" {
			label = option.FriendlyID
		} else {
			label = fmt.Sprintf("%s (%s)", label, option.FriendlyID)
		}
		out = append(out, forms.Option{
			Label: label,
			Value: option.FriendlyID,
		})
	}
	return out
}

func RegisterCommands() {
	bus.RegisterCommand(configPath, "apply", handleApply)
	bus.RegisterCommand(configPath, "refreshModels", handleRefreshModels)
}

func UnregisterCommands() {
	bus.UnregisterCommand(configPath, "apply")
	bus.UnregisterCommand(configPath, "refreshModels")
}

func handleApply(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(*Config)
	if !ok {
		return bus.CommandResult{
			Error:   fmt.Errorf("expected *acp.Config, got %T", cmd.Payload),
			Message: "invalid ACP config payload",
		}
	}
	L_info("acp config: applied", "defaultDriver", cfg.DefaultDriver, "cursorModel", cfg.Drivers.Cursor.Model)
	return bus.CommandResult{
		Success: true,
		Message: "ACP configuration applied",
	}
}

func handleRefreshModels(cmd bus.Command) bus.CommandResult {
	req, ok := cmd.Payload.(RefreshModelsRequest)
	if !ok {
		reqPtr, ok := cmd.Payload.(*RefreshModelsRequest)
		if !ok || reqPtr == nil {
			return bus.CommandResult{
				Error:   fmt.Errorf("expected acp.RefreshModelsRequest, got %T", cmd.Payload),
				Message: "invalid refresh payload",
			}
		}
		req = *reqPtr
	}
	driverID := normalizeModelToken(req.Driver)
	if driverID == "" {
		return bus.CommandResult{
			Error:   fmt.Errorf("driver is required"),
			Message: "driver is required",
		}
	}
	driver, err := configDriver(driverID)
	if err != nil {
		return bus.CommandResult{
			Error:   err,
			Message: err.Error(),
		}
	}
	refresher, ok := driver.(ModelCatalogRefresher)
	if !ok {
		err := fmt.Errorf("ACP driver %q does not support live model refresh", driverID)
		return bus.CommandResult{
			Error:   err,
			Message: err.Error(),
		}
	}
	cwd := defaultConfigRefreshCWD()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	L_info("acp config: refreshing model catalog", "driver", driverID, "cwd", cwd)
	state, err := refresher.RefreshModelCatalog(ctx, ModelCatalogRefreshRequest{CWD: cwd})
	if err != nil {
		L_warn("acp config: refresh model catalog failed", "driver", driverID, "error", err)
		return bus.CommandResult{
			Error:   err,
			Message: fmt.Sprintf("Failed to refresh %s models: %v", driverID, err),
		}
	}
	models := buildFriendlyModelOptions(state)
	L_info("acp config: refreshed model catalog", "driver", driverID, "count", len(models))
	return bus.CommandResult{
		Success: true,
		Message: fmt.Sprintf("Refreshed %d %s models", len(models), driverID),
		Data: RefreshModelsResult{
			Driver: driverID,
			Count:  len(models),
			Models: models,
		},
	}
}

func defaultConfigRefreshCWD() string {
	if mgr := GetManager(); mgr != nil && strings.TrimSpace(mgr.defaultCWD) != "" {
		return mgr.defaultCWD
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

func configDriver(driverID string) (Driver, error) {
	switch normalizeModelToken(driverID) {
	case DriverCursor:
		return NewCursorDriver(), nil
	default:
		return nil, fmt.Errorf("unknown ACP driver: %s", driverID)
	}
}
