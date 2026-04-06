// Package web provides browser-based setup wizard and configuration editor
package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	"github.com/roelfdiedericks/goclaw/internal/configapply"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
	"github.com/roelfdiedericks/goclaw/internal/metadata"
	"github.com/roelfdiedericks/goclaw/internal/voicellm"
)

// API provides JSON API handlers for the setup wizard and editor
type API struct {
	configPath  string
	applyCaller configapply.Caller
	contractErr error
	sections    []SectionCategory
}

// NewAPI creates a new API handler
func NewAPI(configPath string, applyCaller configapply.Caller, sections []SectionCategory) *API {
	registerWebActionCommands()
	return &API{
		configPath:  configPath,
		applyCaller: applyCaller,
		contractErr: ValidateAllSectionContractsStrict(),
		sections:    sections,
	}
}

// HandleSectionAction runs a form action for a section.
func (a *API) HandleSectionAction(w http.ResponseWriter, r *http.Request) {
	if !a.ensureStrictContracts(w) {
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{
			Success: false,
			Message: "Method not allowed",
		})
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/setup/api/section-action/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		writeJSON(w, http.StatusBadRequest, APIResponse{
			Success: false,
			Message: "Section ID and action name required",
		})
		return
	}
	sectionID := parts[0]
	actionName := parts[1]

	section := FindSection(a.sections, sectionID)
	if section == nil {
		writeJSON(w, http.StatusNotFound, APIResponse{
			Success: false,
			Message: "Section not found",
		})
		return
	}
	formDef := GetFormDef(section.ID)
	if formDef == nil {
		writeJSON(w, http.StatusNotFound, APIResponse{
			Success: false,
			Message: "Form definition not found",
		})
		return
	}
	actionDef, ok := findActionDef(*formDef, actionName)
	if !ok {
		writeJSON(w, http.StatusNotFound, APIResponse{
			Success: false,
			Message: "Action not found",
		})
		return
	}

	commandName := actionDef.Name
	if strings.TrimSpace(actionDef.Command) != "" {
		commandName = strings.TrimSpace(actionDef.Command)
	}
	result := bus.SendCommandWithSource(section.ID, commandName, actionDef.Payload, "web", "")
	if result.Error != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{
			Success: false,
			Message: result.Message,
		})
		return
	}
	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: result.Message,
		Data:    result.Data,
	})
}

func findActionDef(def forms.FormDef, actionName string) (forms.ActionDef, bool) {
	for _, action := range def.Actions {
		if action.Name == actionName {
			return action, true
		}
	}
	return forms.ActionDef{}, false
}

func (a *API) ensureStrictContracts(w http.ResponseWriter) bool {
	if a.contractErr == nil {
		return true
	}
	L_error("setup: strict contract violation", "error", a.contractErr)
	writeJSON(w, http.StatusInternalServerError, APIResponse{
		Success: false,
		Message: fmt.Sprintf("strict setup contract violation: %v", a.contractErr),
	})
	return false
}

// APIResponse is the standard JSON response format
type APIResponse struct {
	Success bool              `json:"success"`
	Data    interface{}       `json:"data,omitempty"`
	Errors  map[string]string `json:"errors,omitempty"`
	Message string            `json:"message,omitempty"`
}

// writeJSON writes a JSON response
func writeJSON(w http.ResponseWriter, status int, resp APIResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		L_error("api: failed to write response", "error", err)
	}
}

// HandleGetConfig returns the full configuration as JSON
func (a *API) HandleGetConfig(w http.ResponseWriter, r *http.Request) {
	if !a.ensureStrictContracts(w) {
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{
			Success: false,
			Message: "Method not allowed",
		})
		return
	}

	result, err := a.loadConfig()
	if err != nil {
		L_error("api: failed to load config", "error", err)
		writeJSON(w, http.StatusInternalServerError, APIResponse{
			Success: false,
			Message: "Failed to load configuration",
		})
		return
	}

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    result.Config,
	})
}

// HandleGetSections returns the sidebar sections structure
func (a *API) HandleGetSections(w http.ResponseWriter, r *http.Request) {
	if !a.ensureStrictContracts(w) {
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{
			Success: false,
			Message: "Method not allowed",
		})
		return
	}

	for _, cat := range a.sections {
		for _, item := range cat.Items {
			if err := forms.ValidateJSONPointer(item.ConfigPath); err != nil {
				writeJSON(w, http.StatusInternalServerError, APIResponse{
					Success: false,
					Message: fmt.Sprintf("invalid section config path for %s: %v", item.ID, err),
				})
				return
			}
			if item.Type == SectionTypeFormDef {
				formDef := GetFormDef(item.ID)
				if err := validateSectionFormBinding(&item, formDef); err != nil {
					writeJSON(w, http.StatusInternalServerError, APIResponse{
						Success: false,
						Message: err.Error(),
					})
					return
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    a.sections,
	})
}

// HandleSection handles GET (fetch section config) and POST (save section config)
func (a *API) HandleSection(w http.ResponseWriter, r *http.Request) {
	if !a.ensureStrictContracts(w) {
		return
	}
	// Extract section ID from path
	path := strings.TrimPrefix(r.URL.Path, "/setup/api/section/")
	sectionID := strings.TrimSuffix(path, "/")

	if sectionID == "" {
		writeJSON(w, http.StatusBadRequest, APIResponse{
			Success: false,
			Message: "Section ID required",
		})
		return
	}

	section := FindSection(a.sections, sectionID)
	if section == nil {
		writeJSON(w, http.StatusNotFound, APIResponse{
			Success: false,
			Message: "Section not found",
		})
		return
	}

	switch r.Method {
	case http.MethodGet:
		a.getSectionConfig(w, section)
	case http.MethodPost:
		a.saveSectionConfig(w, r, section)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{
			Success: false,
			Message: "Method not allowed",
		})
	}
}

func (a *API) getSectionConfig(w http.ResponseWriter, section *SectionItem) {
	if err := forms.ValidateJSONPointer(section.ConfigPath); err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{
			Success: false,
			Message: fmt.Sprintf("invalid section config path for %s: %v", section.ID, err),
		})
		return
	}

	result, err := a.loadConfig()
	if err != nil {
		L_error("api: failed to load config", "error", err)
		writeJSON(w, http.StatusInternalServerError, APIResponse{
			Success: false,
			Message: "Failed to load configuration",
		})
		return
	}

	// Extract section data from config using JSON pointer.
	sectionData, err := extractConfigPath(result.Config, section.ConfigPath)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{
			Success: false,
			Message: err.Error(),
		})
		return
	}

	// Get FormDef and render to HTML
	var formHTML string
	if section.Type == SectionTypeFormDef {
		formDef := GetFormDef(section.ID)
		if section.ID == "media" {
			mediaDef := media.ConfigFormDefWithValues(result.Config.Media)
			formDef = &mediaDef
		}
		if section.ID == "voicellm" {
			sectionData = result.Config.VoiceLLM.ToConfigFormData()
		}
		if formDef != nil {
			if err := validateSectionFormBinding(section, formDef); err != nil {
				writeJSON(w, http.StatusInternalServerError, APIResponse{
					Success: false,
					Message: err.Error(),
				})
				return
			}
			html, err := RenderFormHTML(*formDef, "formData")
			if err != nil {
				L_warn("api: failed to render form", "section", section.ID, "error", err)
			} else {
				formHTML = string(html)
			}
			// Root-backed composite sections should only expose keys they actually own.
			// This prevents accidental root-wide edits and keeps save payloads deterministic.
			if section.ConfigPath == "/" {
				sectionData = projectSectionRootData(sectionData, formDef)
			}
		}
	}

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"section":  section,
			"config":   sectionData,
			"formHTML": formHTML,
		},
	})
}

func (a *API) saveSectionConfig(w http.ResponseWriter, r *http.Request, section *SectionItem) {
	var payload map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{
			Success: false,
			Message: "Invalid JSON payload",
		})
		return
	}
	if section.ID == "voicellm" {
		runtimePayload, err := expandVoiceLLMSectionPayload(payload)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, APIResponse{
				Success: false,
				Message: err.Error(),
			})
			return
		}
		payload = runtimePayload
	}

	// Enforce root payload allowlist for composite root-backed FormDef sections.
	if section.ConfigPath == "/" && section.Type == SectionTypeFormDef {
		if err := validateRootPayload(section, payload); err != nil {
			writeJSON(w, http.StatusBadRequest, APIResponse{
				Success: false,
				Message: err.Error(),
			})
			return
		}
	}
	if err := validateSectionPayloadAgainstSchema(section, payload); err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{
			Success: false,
			Message: err.Error(),
		})
		return
	}

	// Load current config
	result, err := a.loadConfig()
	if err != nil {
		L_error("api: failed to load config", "error", err)
		writeJSON(w, http.StatusInternalServerError, APIResponse{
			Success: false,
			Message: "Failed to load configuration",
		})
		return
	}

	// Update section in config
	if err := setConfigPath(result.Config, section.ConfigPath, payload); err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{
			Success: false,
			Message: err.Error(),
		})
		return
	}

	// Save config to the same path it was loaded from
	savePath := a.resolveSavePath(result)

	if err := config.BackupAndWriteJSON(savePath, result.Config, config.DefaultBackupCount); err != nil {
		L_error("api: failed to save config", "error", err)
		writeJSON(w, http.StatusInternalServerError, APIResponse{
			Success: false,
			Message: "Failed to save configuration",
		})
		return
	}

	L_info("api: saved section config", "section", section.ID, "path", savePath)

	// TODO: Trigger Apply command via bus
	// bus.SendCommand(section.ID, "apply", nil)

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Configuration saved",
	})
}

func expandVoiceLLMSectionPayload(payload map[string]interface{}) (map[string]interface{}, error) {
	var formData voicellm.ConfigFormData
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal VoiceLLM payload: %w", err)
	}
	if err := json.Unmarshal(data, &formData); err != nil {
		return nil, fmt.Errorf("invalid VoiceLLM payload: %w", err)
	}
	runtimeCfg := formData.ToConfig()
	runtimeData, err := json.Marshal(runtimeCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal VoiceLLM runtime config: %w", err)
	}
	var runtimePayload map[string]interface{}
	if err := json.Unmarshal(runtimeData, &runtimePayload); err != nil {
		return nil, fmt.Errorf("failed to decode VoiceLLM runtime payload: %w", err)
	}
	return runtimePayload, nil
}

// HandleApply determines how saved config should take effect.
func (a *API) HandleApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{
			Success: false,
			Message: "Method not allowed",
		})
		return
	}

	applyResult := configapply.Decide(a.applyCaller)
	L_info("api: apply decision",
		"caller", a.applyCaller,
		"mode", applyResult.RuntimeMode,
		"action", applyResult.Action,
		"restartRequired", applyResult.RestartRequired,
	)

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"apply": applyResult,
		},
		Message: applyResult.Message,
	})
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	if applyResult.Action == configapply.ActionSupervisedRestart &&
		applyResult.RestartCapability == configapply.RestartCapabilityAuto {
		if err := configapply.ScheduleSupervisorRestart(750 * time.Millisecond); err != nil {
			L_error("api: failed to schedule supervised restart", "error", err)
		}
	}
}

// extractConfigPath extracts a nested value from config using JSON Pointer.
func extractConfigPath(cfg *config.Config, pointer string) (interface{}, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}

	return forms.JSONPointerGet(m, pointer)
}

// setConfigPath sets a nested value in config using JSON Pointer.
func setConfigPath(cfg *config.Config, pointer string, value interface{}) error {
	if err := forms.ValidateJSONPointer(pointer); err != nil {
		return err
	}

	// Convert config to map, update by pointer, convert back.
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}

	// Root writes are merged (non-destructive), not replaced.
	if pointer == "/" {
		mv, ok := value.(map[string]interface{})
		if !ok {
			return fmt.Errorf("root update expects object payload")
		}
		for k, v := range mv {
			m[k] = v
		}
	} else {
		if err := forms.JSONPointerSet(m, pointer, value); err != nil {
			return err
		}
	}

	// Convert back to config
	updated, err := json.Marshal(m)
	if err != nil {
		return err
	}

	return json.Unmarshal(updated, cfg)
}

func (a *API) loadConfig() (*config.LoadResult, error) {
	var (
		result *config.LoadResult
		err    error
	)

	if strings.TrimSpace(a.configPath) == "" {
		result, err = config.Load()
	} else {
		result, err = config.LoadFromPath(a.configPath)
	}
	if err == nil {
		return result, nil
	}
	if config.IsMissingOrIncompleteConfigError(err) {
		return config.LoadDefaults()
	}
	return nil, err
}

func (a *API) resolveSavePath(result *config.LoadResult) string {
	if result != nil && strings.TrimSpace(result.SourcePath) != "" {
		return result.SourcePath
	}
	if strings.TrimSpace(a.configPath) != "" {
		return a.configPath
	}
	return config.GetLoadedConfigPath()
}

func projectSectionRootData(sectionData interface{}, formDef *forms.FormDef) interface{} {
	rootMap, ok := sectionData.(map[string]interface{})
	if !ok || formDef == nil {
		return sectionData
	}
	allowed := collectSectionRootKeys(formDef.Sections, "")
	if len(allowed) == 0 {
		return map[string]interface{}{}
	}
	out := make(map[string]interface{})
	for k := range allowed {
		if v, exists := rootMap[k]; exists {
			out[k] = v
		}
	}
	return out
}

func validateRootPayload(section *SectionItem, payload map[string]interface{}) error {
	formDef := GetFormDef(section.ID)
	if formDef == nil {
		return fmt.Errorf("section %q has no form definition", section.ID)
	}
	allowed := collectSectionRootKeys(formDef.Sections, "")
	if len(allowed) == 0 {
		return fmt.Errorf("section %q has no writable root keys", section.ID)
	}
	for k := range payload {
		if !allowed[k] {
			return fmt.Errorf("field %q is not writable in section %q", k, section.ID)
		}
	}
	return nil
}

func validateSectionPayloadAgainstSchema(section *SectionItem, payload map[string]interface{}) error {
	if section == nil || section.Type != SectionTypeFormDef {
		return nil
	}

	rt, err := sectionRootType(section.ConfigPath)
	if err != nil {
		return err
	}
	for rt.Kind() == reflect.Ptr {
		rt = rt.Elem()
	}

	// Maps are intentionally dynamic for keyed config blocks (e.g. providers, roles);
	// unknown-key rejection is enforced by the map value schema.
	target := reflect.New(rt).Interface()
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return fmt.Errorf("invalid payload for section %q: %w", section.ID, err)
	}
	return nil
}

func collectSectionRootKeys(sections []forms.Section, prefix string) map[string]bool {
	keys := make(map[string]bool)
	for _, sec := range sections {
		secPrefix := prefix
		if sec.FieldName != "" {
			secPrefix = joinFieldPathLocal(prefix, sec.FieldName)
		}
		for _, field := range sec.Fields {
			fieldPath := joinFieldPathLocal(secPrefix, field.Name)
			parts := strings.Split(fieldPath, ".")
			if len(parts) > 0 && parts[0] != "" {
				keys[parts[0]] = true
			}
		}
		if sec.Nested != nil {
			nested := collectSectionRootKeys(sec.Nested.Sections, secPrefix)
			for k := range nested {
				keys[k] = true
			}
		}
	}
	return keys
}

func joinFieldPathLocal(prefix, name string) string {
	name = strings.TrimSpace(name)
	if prefix == "" {
		return name
	}
	if name == "" {
		return prefix
	}
	return prefix + "." + name
}

func setupPurpose(r *http.Request) string {
	return strings.ToLower(strings.TrimSpace(r.URL.Query().Get("purpose")))
}

func isLikelyEmbeddingModelID(modelID string) bool {
	id := strings.ToLower(strings.TrimSpace(modelID))
	if id == "" {
		return false
	}
	keywords := []string{
		"embed",
		"embedding",
		"minilm",
		"nomic-embed",
		"mxbai",
		"bge",
		"e5",
		"gte",
		"jina-emb",
	}
	for _, kw := range keywords {
		if strings.Contains(id, kw) {
			return true
		}
	}
	return false
}

// HandleGetProviders returns configured LLM provider aliases with metadata
func (a *API) HandleGetProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{
			Success: false,
			Message: "Method not allowed",
		})
		return
	}

	result, err := a.loadConfig()
	if err != nil {
		L_error("api: failed to load config", "error", err)
		writeJSON(w, http.StatusInternalServerError, APIResponse{
			Success: false,
			Message: "Failed to load configuration",
		})
		return
	}

	meta := metadata.Get()
	var providers []map[string]interface{}
	purpose := setupPurpose(r)

	// Get sorted alias names
	aliases := make([]string, 0, len(result.Config.LLM.Providers))
	for alias := range result.Config.LLM.Providers {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)

	for _, alias := range aliases {
		provCfg := result.Config.LLM.Providers[alias]
		if purpose == "embeddings" && !llm.DriverSupportsEmbeddings(provCfg.Driver) {
			continue
		}
		if purpose != "embeddings" && provCfg.EmbeddingOnly {
			continue
		}

		// Resolve to metadata provider ID
		providerID := meta.ResolveProvider(provCfg.Subtype, provCfg.Driver, provCfg.BaseURL)

		entry := map[string]interface{}{
			"alias":      alias,
			"driver":     provCfg.Driver,
			"providerID": providerID,
		}

		// Add metadata provider info if available
		if prov, ok := meta.GetModelProvider(providerID); ok {
			entry["name"] = prov.Name
			entry["modelCount"] = len(prov.Models)
		} else {
			entry["name"] = alias
			entry["modelCount"] = 0
		}

		providers = append(providers, entry)
	}

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    map[string]interface{}{"providers": providers},
	})
}

// HandleGetModels returns full model details for a provider alias
func (a *API) HandleGetModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{
			Success: false,
			Message: "Method not allowed",
		})
		return
	}

	// Extract alias from path: /setup/api/models/{alias}
	alias := strings.TrimPrefix(r.URL.Path, "/setup/api/models/")
	if alias == "" {
		writeJSON(w, http.StatusBadRequest, APIResponse{
			Success: false,
			Message: "Provider alias required",
		})
		return
	}

	result, err := a.loadConfig()
	if err != nil {
		L_error("api: failed to load config", "error", err)
		writeJSON(w, http.StatusInternalServerError, APIResponse{
			Success: false,
			Message: "Failed to load configuration",
		})
		return
	}

	provCfg, ok := result.Config.LLM.Providers[alias]
	if !ok {
		writeJSON(w, http.StatusNotFound, APIResponse{
			Success: false,
			Message: "Provider alias not found",
		})
		return
	}

	meta := metadata.Get()
	providerID := meta.ResolveProvider(provCfg.Subtype, provCfg.Driver, provCfg.BaseURL)
	purpose := setupPurpose(r)

	if purpose == "embeddings" && !llm.DriverSupportsEmbeddings(provCfg.Driver) {
		writeJSON(w, http.StatusOK, APIResponse{
			Success: true,
			Data: map[string]interface{}{
				"alias":        alias,
				"providerID":   providerID,
				"providerName": alias,
				"models":       []map[string]interface{}{},
				"defaultModel": "",
			},
		})
		return
	}

	// Get provider name
	providerName := alias
	if prov, ok := meta.GetModelProvider(providerID); ok {
		providerName = prov.Name
	}

	// Metadata currently tracks chat models. For embeddings purpose, prefer live model
	// listing and avoid exposing chat catalogs as embedding options.
	modelIDs := meta.GetKnownChatModels(providerID)
	if purpose == "embeddings" {
		modelIDs = nil
	}
	defaultLarge, _ := meta.GetDefaultModels(providerID)

	if len(modelIDs) == 0 {
		provider, err := llm.NewProvider(alias, provCfg)
		if err == nil {
			if lister, ok := provider.(llm.ModelLister); ok {
				liveModels, listErr := lister.ListModels(r.Context())
				if listErr == nil && len(liveModels) > 0 {
					var models []map[string]interface{}
					defaultSet := false
					for _, model := range liveModels {
						if purpose == "embeddings" && alias != llm.BuiltInHugotProviderAlias && !isLikelyEmbeddingModelID(model.ID) {
							continue
						}
						entry := map[string]interface{}{
							"id":   model.ID,
							"name": model.DisplayName,
						}
						if entry["name"] == "" {
							entry["name"] = model.ID
						}
						if model.ContextTokens > 0 {
							entry["contextWindow"] = model.ContextTokens
						}
						if !defaultSet {
							entry["isDefault"] = true
							defaultLarge = model.ID
							defaultSet = true
						}
						models = append(models, entry)
					}

					writeJSON(w, http.StatusOK, APIResponse{
						Success: true,
						Data: map[string]interface{}{
							"alias":        alias,
							"providerID":   providerID,
							"providerName": providerName,
							"models":       models,
							"defaultModel": defaultLarge,
						},
					})
					return
				}
			}
		}
	}

	var models []map[string]interface{}
	for _, modelID := range modelIDs {
		entry := map[string]interface{}{
			"id":        modelID,
			"isDefault": modelID == defaultLarge,
		}

		if m, ok := meta.GetModel(providerID, modelID); ok {
			entry["name"] = m.Name
			entry["contextWindow"] = m.ContextWindow
			entry["maxOutputTokens"] = m.MaxOutputTokens
			entry["cost"] = map[string]interface{}{
				"input":      m.Cost.Input,
				"output":     m.Cost.Output,
				"cacheRead":  m.Cost.CacheRead,
				"cacheWrite": m.Cost.CacheWrite,
			}
			entry["capabilities"] = map[string]interface{}{
				"vision":           m.Capabilities.Vision,
				"toolUse":          m.Capabilities.ToolUse,
				"reasoning":        m.Capabilities.Reasoning,
				"structuredOutput": m.Capabilities.StructuredOutput,
			}
			entry["knowledgeCutoff"] = m.Metadata.KnowledgeCutoff
			entry["openWeights"] = m.Metadata.OpenWeights
		} else {
			entry["name"] = modelID
		}

		models = append(models, entry)
	}

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"alias":        alias,
			"providerID":   providerID,
			"providerName": providerName,
			"models":       models,
			"defaultModel": defaultLarge,
		},
	})
}

// HandleGetPresets returns available LLM provider presets from models.json
func (a *API) HandleGetPresets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{
			Success: false,
			Message: "Method not allowed",
		})
		return
	}

	meta := metadata.Get()
	providerIDs := meta.ModelProviderIDs()

	var presets []map[string]interface{}

	for _, pid := range providerIDs {
		prov, ok := meta.GetModelProvider(pid)
		if !ok {
			continue
		}

		preset := map[string]interface{}{
			"id":          pid,
			"name":        prov.Name,
			"driver":      prov.Driver,
			"apiEndpoint": prov.APIEndpoint,
			"modelCount":  len(prov.Models),
			"isLocal":     llm.DriverOrEndpointIsLocal(prov.Driver, prov.APIEndpoint),
		}

		// Get default model for this provider
		defaultLarge, _ := meta.GetDefaultModels(pid)
		if defaultLarge != "" {
			preset["defaultModel"] = defaultLarge
		}

		presets = append(presets, preset)
	}

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    map[string]interface{}{"presets": presets},
	})
}

// HandleGetDrivers returns supported runtime LLM drivers from the llm registry.
func (a *API) HandleGetDrivers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{
			Success: false,
			Message: "Method not allowed",
		})
		return
	}

	descriptors := llm.ListDrivers()
	drivers := make([]map[string]interface{}, 0, len(descriptors))
	for _, d := range descriptors {
		label := d.Label
		if strings.TrimSpace(label) == "" {
			label = d.ID
		}
		drivers = append(drivers, map[string]interface{}{
			"id":                 d.ID,
			"label":              label,
			"supportsEmbeddings": d.SupportsEmbeddings,
		})
	}

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    map[string]interface{}{"drivers": drivers},
	})
}
