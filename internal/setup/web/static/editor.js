// Bootstrap/jQuery runtime for the GoClaw setup editor and wizard.
(function (window, $) {
    'use strict';

    const PROVIDER_DRIVER_OPTIONS = [
        { value: 'anthropic', label: 'Anthropic' },
        { value: 'openai', label: 'OpenAI Compatible' },
        { value: 'oai-next', label: 'OpenAI (Next)' },
        { value: 'ollama', label: 'Ollama' },
        { value: 'xai', label: 'xAI' }
    ];

    const THINKING_LEVEL_OPTIONS = [
        { value: '', label: '(default)' },
        { value: 'off', label: 'Off' },
        { value: 'minimal', label: 'Minimal' },
        { value: 'low', label: 'Low' },
        { value: 'medium', label: 'Medium' },
        { value: 'high', label: 'High' },
        { value: 'xhigh', label: 'Extra High' }
    ];

    const MEMORY_OPTIONS = [
        { value: 'full', label: 'Full' },
        { value: 'none', label: 'None' }
    ];

    const TRANSCRIPT_OPTIONS = [
        { value: 'all', label: 'All' },
        { value: 'own', label: 'Own only' },
        { value: 'none', label: 'None' }
    ];

    function deepClone(value) {
        return JSON.parse(JSON.stringify(value == null ? {} : value));
    }

    function escapeHtml(value) {
        return String(value == null ? '' : value)
            .replaceAll('&', '&amp;')
            .replaceAll('<', '&lt;')
            .replaceAll('>', '&gt;')
            .replaceAll('"', '&quot;')
            .replaceAll("'", '&#39;');
    }

    function getByPath(obj, path) {
        if (!path) return obj;
        const parts = String(path).split('.').filter(Boolean);
        let current = obj;
        for (const part of parts) {
            if (current == null) return undefined;
            current = current[part];
        }
        return current;
    }

    function setByPath(obj, path, value) {
        const parts = String(path).split('.').filter(Boolean);
        if (parts.length === 0) return;
        let current = obj;
        for (let i = 0; i < parts.length - 1; i += 1) {
            const part = parts[i];
            if (current[part] == null || typeof current[part] !== 'object' || Array.isArray(current[part])) {
                current[part] = {};
            }
            current = current[part];
        }
        current[parts[parts.length - 1]] = value;
    }

    function parseStringList(value) {
        return String(value || '')
            .split(',')
            .map(s => s.trim())
            .filter(Boolean);
    }

    function formatStringList(value) {
        if (!Array.isArray(value)) return '';
        return value.join(', ');
    }

    function parseShowWhen(expr) {
        const raw = String(expr || '').trim();
        if (!raw) return [];
        return raw.split(',').map(part => part.trim()).filter(Boolean).map(part => {
            const neqIdx = part.indexOf('!=');
            if (neqIdx > 0) {
                return {
                    fieldPath: part.slice(0, neqIdx).trim(),
                    operator: '!=',
                    value: part.slice(neqIdx + 2).trim()
                };
            }
            const eqIdx = part.indexOf('=');
            if (eqIdx > 0) {
                return {
                    fieldPath: part.slice(0, eqIdx).trim(),
                    operator: '=',
                    value: part.slice(eqIdx + 1).trim()
                };
            }
            throw new Error(`invalid showWhen clause "${part}"`);
        });
    }

    function compareShowWhen(actual, expected, operator) {
        let left = actual;
        if (typeof left === 'boolean') {
            left = left ? 'true' : 'false';
        } else if (left == null) {
            left = '';
        } else {
            left = String(left);
        }
        return operator === '!=' ? left !== expected : left === expected;
    }

    function evaluateShowWhen(expr, state) {
        if (!expr) return true;
        let clauses;
        try {
            clauses = parseShowWhen(expr);
        } catch (_err) {
            return false;
        }
        if (!clauses.length) return true;
        return clauses.some(clause => compareShowWhen(getByPath(state, clause.fieldPath), clause.value, clause.operator));
    }

    function showAlert($alert, message) {
        if (!message) {
            $alert.addClass('d-none');
            $alert.find('.js-alert-text').text('');
            return;
        }
        $alert.removeClass('d-none');
        $alert.find('.js-alert-text').text(message);
    }

    function hideAlert($alert) {
        $alert.addClass('d-none');
        $alert.find('.js-alert-text').text('');
    }

    class SetupEditorController {
        constructor(root) {
            this.$root = $(root);
            this.$dashboard = $('#editor-dashboard');
            this.$sectionPanel = $('#editor-section-panel');
            this.$loading = $('#editor-loading');
            this.$title = $('#editor-section-title');
            this.$errorAlert = $('#editor-error-alert');
            this.$successAlert = $('#editor-success-alert');
            this.$formContent = $('#editor-form-content');
            this.$usersContent = $('#editor-users-content');
            this.$topActions = $('#editor-top-actions');
            this.$topDirtyLabel = $('#editor-top-dirty-label');
            this.$topSave = $('#editor-top-save');
            this.$topDiscard = $('#editor-top-discard');

            this.currentSection = '';
            this.currentTitle = 'Select a section';
            this.currentSectionType = '';
            this.formData = {};
            this.originalData = {};
            this.sectionDrafts = {};
            this.sectionOriginals = {};
            this.dirtyState = {};
            this.fieldErrors = {};
            this.loading = false;
            this.saving = false;

            this.mcMeta = {};
            this.mcLoading = {};
            this.mcExpanded = {};
            this.mcProviders = [];
            this.mcProvidersLoaded = false;
            this.mcModalField = '';
            this.mcModalStep = 1;
            this.mcSelectedProvider = null;
            this.mcSelectedModel = null;
            this.mcAvailableModels = [];
            this.mcModelSearch = '';
            this.mcDrag = null;
            this.mcModal = new bootstrap.Modal(document.getElementById('mcModal'));

            this.providerUi = {};
            this.providerPresets = [];
            this.providerPresetsLoaded = false;

            this.rolesUi = {};

            this.users = [];
            this.userRoles = [];
            this.userForm = {};
            this.userFormErrors = {};
            this.userEditing = false;
            this.usersLoading = false;
            this.usersError = '';
            this.usersSuccess = '';
            this.userDeleting = false;
            this.userDeleteUsername = '';
            this.userModal = null;
            this.userDeleteModal = null;
        }

        init() {
            window.addEventListener('beforeunload', (event) => {
                if (this.hasAnyDirty()) {
                    event.preventDefault();
                    event.returnValue = '';
                }
            });

            this.bindStaticEvents();
            this.syncTopBar();
        }

        bindStaticEvents() {
            this.$root.on('click', '.sidebar-item', (event) => {
                event.preventDefault();
                const sectionId = $(event.currentTarget).data('section-id');
                if (sectionId) this.switchSection(sectionId);
            });

            this.$root.on('click keydown', '.js-quick-task', (event) => {
                if (event.type === 'keydown' && event.key !== 'Enter' && event.key !== ' ') {
                    return;
                }
                event.preventDefault();
                const sectionId = $(event.currentTarget).data('section-target');
                if (sectionId) this.switchSection(sectionId);
            });

            this.$topSave.on('click', () => this.saveAll());
            this.$topDiscard.on('click', () => this.discardAll());

            this.$errorAlert.find('.btn-close').on('click', () => hideAlert(this.$errorAlert));
            this.$successAlert.find('.btn-close').on('click', () => hideAlert(this.$successAlert));

            this.$formContent.on('input change', '.js-bound-field', (event) => this.handleBoundFieldChange(event));
            this.$formContent.on('click', '.js-model-chain-add', (event) => this.openModelModal($(event.currentTarget).data('field-path')));
            this.$formContent.on('click', '.js-model-remove', (event) => this.removeModel($(event.currentTarget).data('field-path'), Number($(event.currentTarget).data('index'))));
            this.$formContent.on('click', '.js-model-toggle', (event) => this.toggleModelExpanded($(event.currentTarget).data('model-ref')));
            this.$formContent.on('dragstart', '[data-mc-item]', (event) => this.handleModelDragStart(event));
            this.$formContent.on('dragover', '[data-mc-item], .js-model-chain-add', (event) => this.handleModelDragOver(event));
            this.$formContent.on('dragenter', '[data-mc-item], .js-model-chain-add', (event) => $(event.currentTarget).addClass('mc-drag-over'));
            this.$formContent.on('dragleave', '[data-mc-item], .js-model-chain-add', (event) => $(event.currentTarget).removeClass('mc-drag-over'));
            this.$formContent.on('drop', '[data-mc-item], .js-model-chain-add', (event) => this.handleModelDrop(event));
            this.$formContent.on('dragend', '[data-mc-item]', () => this.clearModelDragState());

            this.$formContent.on('click', '.js-provider-toggle', (event) => this.toggleProvider($(event.currentTarget).data('field-path'), $(event.currentTarget).data('alias')));
            this.$formContent.on('click', '.js-provider-delete', (event) => this.deleteProvider($(event.currentTarget).data('field-path'), $(event.currentTarget).data('alias')));
            this.$formContent.on('click', '.js-provider-toggle-key', (event) => this.toggleProviderKey($(event.currentTarget).data('field-path'), $(event.currentTarget).data('alias')));
            this.$formContent.on('click', '.js-provider-add-start', (event) => this.startAddProvider($(event.currentTarget).data('field-path')));
            this.$formContent.on('click', '.js-provider-add-cancel', (event) => this.cancelAddProvider($(event.currentTarget).data('field-path')));
            this.$formContent.on('input', '.js-provider-preset-filter', (event) => this.updateProviderPresetFilter($(event.currentTarget).data('field-path'), $(event.currentTarget).val()));
            this.$formContent.on('click', '.js-provider-preset-select', (event) => this.selectProviderPreset($(event.currentTarget).data('field-path'), $(event.currentTarget).data('preset-id')));
            this.$formContent.on('input change', '.js-provider-input', (event) => this.handleProviderInput(event));
            this.$formContent.on('click', '.js-provider-save-new', (event) => this.saveNewProvider($(event.currentTarget).data('field-path')));

            this.$formContent.on('click', '.js-role-toggle', (event) => this.toggleRole($(event.currentTarget).data('field-path'), $(event.currentTarget).data('role-name')));
            this.$formContent.on('click', '.js-role-delete', (event) => this.deleteRole($(event.currentTarget).data('field-path'), $(event.currentTarget).data('role-name')));
            this.$formContent.on('click', '.js-role-add-start', (event) => this.startAddRole($(event.currentTarget).data('field-path')));
            this.$formContent.on('click', '.js-role-add-cancel', (event) => this.cancelAddRole($(event.currentTarget).data('field-path')));
            this.$formContent.on('input change', '.js-role-input', (event) => this.handleRoleInput(event));
            this.$formContent.on('click', '.js-role-save-new', (event) => this.saveNewRole($(event.currentTarget).data('field-path')));

            $('#mcModalBack').on('click', () => this.showModelProviderStep());
            $('#mcModalAdd').on('click', () => this.addSelectedModelToChain());
            $('#mcModelSearch').on('input', (event) => {
                this.mcModelSearch = $(event.currentTarget).val() || '';
                this.renderModelModal();
            });
            $('#mcProviderList').on('click', '.js-model-provider-select', (event) => {
                const alias = $(event.currentTarget).data('provider-alias');
                this.selectModelProvider(alias);
            });
            $('#mcModelList').on('click', '.js-model-select', (event) => {
                const modelId = $(event.currentTarget).data('model-id');
                this.selectModelByID(modelId);
            });
        }

        hasAnyDirty() {
            return Object.values(this.dirtyState).some(Boolean);
        }

        dirtyCount() {
            return Object.values(this.dirtyState).filter(Boolean).length;
        }

        currentDirty() {
            if (!this.currentSection || this.currentSectionType === 'custom') return false;
            return JSON.stringify(this.formData) !== JSON.stringify(this.originalData);
        }

        syncTopBar() {
            const show = this.hasAnyDirty();
            this.$topActions.toggleClass('d-none', !show);
            this.$topSave.prop('disabled', !show || this.saving);
            this.$topDiscard.prop('disabled', !show || this.saving);

            if (!show) {
                this.$topDirtyLabel.text('');
            } else if (this.dirtyCount() === 1 && this.currentSection && this.dirtyState[this.currentSection]) {
                this.$topDirtyLabel.text(this.currentTitle ? `Unsaved: ${this.currentTitle}` : 'Unsaved changes');
            } else {
                this.$topDirtyLabel.text(`Unsaved: ${this.dirtyCount()} sections`);
            }

            $('.js-sidebar-dirty-indicator').each((_, el) => {
                const $el = $(el);
                const sectionId = $el.data('section-id');
                $el.toggleClass('d-none', !this.dirtyState[sectionId]);
            });
        }

        markSidebarActive() {
            $('.sidebar-item').removeClass('active');
            if (this.currentSection) {
                $(`.sidebar-item[data-section-id="${this.currentSection}"]`).addClass('active');
            }
        }

        cacheCurrentSectionState() {
            if (!this.currentSection || this.currentSectionType === 'custom') return;
            this.sectionDrafts[this.currentSection] = deepClone(this.formData);
            if (!this.sectionOriginals[this.currentSection]) {
                this.sectionOriginals[this.currentSection] = deepClone(this.originalData);
            }
            this.dirtyState[this.currentSection] = this.currentDirty();
        }

        async switchSection(sectionId) {
            if (this.currentSection === sectionId) return;
            this.cacheCurrentSectionState();
            await this.loadSection(sectionId);
        }

        async loadSection(sectionId) {
            this.loading = true;
            this.fieldErrors = {};
            showAlert(this.$errorAlert, '');
            showAlert(this.$successAlert, '');
            this.$loading.removeClass('d-none');
            this.$dashboard.addClass('d-none');
            this.$sectionPanel.removeClass('d-none');

            try {
                const resp = await fetch(`/setup/api/section/${sectionId}`);
                const data = await resp.json();
                if (!data.success) throw new Error(data.message || 'Failed to load section');

                this.currentSection = sectionId;
                this.currentTitle = data.data.section.label;
                this.currentSectionType = data.data.section.type || 'formdef';
                this.$title.text(this.currentTitle);
                this.markSidebarActive();

                if (this.currentSectionType === 'custom') {
                    this.formData = {};
                    this.originalData = {};
                    this.$formContent.empty().addClass('d-none');
                    this.$usersContent.removeClass('d-none');
                    if (sectionId === 'users') {
                        await this.loadUsers();
                    }
                } else {
                    const serverConfig = data.data.config || {};
                    const hasDraft = !!this.sectionDrafts[sectionId];
                    if (hasDraft) {
                        this.formData = deepClone(this.sectionDrafts[sectionId]);
                        this.originalData = deepClone(this.sectionOriginals[sectionId] || serverConfig);
                    } else {
                        this.formData = deepClone(serverConfig);
                        this.originalData = deepClone(serverConfig);
                        this.sectionDrafts[sectionId] = deepClone(serverConfig);
                        this.sectionOriginals[sectionId] = deepClone(serverConfig);
                        this.dirtyState[sectionId] = false;
                    }

                    const formHTML = data.data.formHTML || `<pre>${escapeHtml(JSON.stringify(this.formData, null, 2))}</pre>`;
                    this.$usersContent.addClass('d-none').empty();
                    this.$formContent.html(formHTML).removeClass('d-none');
                    this.renderCurrentForm();
                }
            } catch (err) {
                showAlert(this.$errorAlert, err.message || 'Failed to load section');
            } finally {
                this.loading = false;
                this.$loading.addClass('d-none');
                this.syncTopBar();
            }
        }

        renderCurrentForm() {
            this.populateBoundFields(this.$formContent, this.formData);
            this.applyShowWhen(this.$formContent, this.formData);
            this.renderFieldErrors(this.$formContent, this.fieldErrors);
            this.initModelWidgets();
            this.renderProviderLists();
            this.renderRolesLists();
        }

        populateBoundFields($container, state) {
            $container.find('.js-bound-field').each((_, el) => {
                const $field = $(el);
                const bindPath = $field.data('bind');
                const bindType = $field.data('bind-type') || 'string';
                const value = getByPath(state, bindPath);
                if ($field.is(':checkbox')) {
                    $field.prop('checked', !!value);
                } else if (bindType === 'string-list') {
                    $field.val(formatStringList(value));
                } else if (bindType === 'number') {
                    $field.val(value == null ? '' : value);
                } else {
                    $field.val(value == null ? '' : value);
                }
            });
        }

        applyShowWhen($container, state) {
            $container.find('[data-showwhen]').each((_, el) => {
                const $el = $(el);
                const expr = $el.data('showwhen');
                $el.toggleClass('d-none', !evaluateShowWhen(expr, state));
            });
        }

        renderFieldErrors($container, errors) {
            $container.find('.js-bound-field').removeClass('is-invalid');
            $container.find('[data-field-error]').each((_, el) => {
                const $error = $(el);
                const fieldPath = $error.data('field-error');
                const message = errors[fieldPath];
                $error.toggleClass('d-none', !message).text(message || '');
                if (message) {
                    $container.find(`.js-bound-field[data-bind="${fieldPath}"]`).addClass('is-invalid');
                }
            });
        }

        handleBoundFieldChange(event) {
            const $field = $(event.currentTarget);
            const bindPath = $field.data('bind');
            const bindType = $field.data('bind-type') || 'string';

            let value;
            if ($field.is(':checkbox')) {
                value = $field.is(':checked');
            } else if (bindType === 'number') {
                const raw = $field.val();
                value = raw === '' ? 0 : Number(raw);
            } else if (bindType === 'string-list') {
                value = parseStringList($field.val());
            } else {
                value = $field.val();
            }

            setByPath(this.formData, bindPath, value);
            this.sectionDrafts[this.currentSection] = deepClone(this.formData);
            this.dirtyState[this.currentSection] = this.currentDirty();
            this.fieldErrors[bindPath] = '';
            this.applyShowWhen(this.$formContent, this.formData);
            this.renderFieldErrors(this.$formContent, this.fieldErrors);
            this.syncTopBar();
        }

        async saveAll() {
            const dirtySections = Object.keys(this.dirtyState).filter(sectionId => this.dirtyState[sectionId]);
            if (!dirtySections.length) return;

            this.cacheCurrentSectionState();
            this.saving = true;
            showAlert(this.$errorAlert, '');
            showAlert(this.$successAlert, '');
            this.fieldErrors = {};
            this.syncTopBar();

            try {
                for (const sectionId of dirtySections) {
                    const payload = sectionId === this.currentSection && this.currentSectionType !== 'custom'
                        ? this.formData
                        : this.sectionDrafts[sectionId];
                    if (!payload) continue;

                    const resp = await fetch(`/setup/api/section/${sectionId}`, {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify(payload)
                    });
                    const data = await resp.json();
                    if (!data.success) {
                        if (sectionId === this.currentSection && data.errors) {
                            this.fieldErrors = data.errors;
                            this.renderFieldErrors(this.$formContent, this.fieldErrors);
                            throw new Error('Please fix the errors below');
                        }
                        throw new Error(data.message || `Failed to save section: ${sectionId}`);
                    }

                    const savedCopy = deepClone(payload);
                    this.sectionOriginals[sectionId] = savedCopy;
                    this.sectionDrafts[sectionId] = deepClone(payload);
                    this.dirtyState[sectionId] = false;
                    if (sectionId === this.currentSection && this.currentSectionType !== 'custom') {
                        this.originalData = deepClone(payload);
                    }
                }

                showAlert(this.$successAlert, dirtySections.length === 1
                    ? 'Configuration saved successfully'
                    : `Configuration saved successfully (${dirtySections.length} sections)`);
            } catch (err) {
                showAlert(this.$errorAlert, err.message || 'Failed to save configuration');
            } finally {
                this.saving = false;
                this.syncTopBar();
            }
        }

        async discardAll() {
            showAlert(this.$errorAlert, '');
            showAlert(this.$successAlert, '');
            this.fieldErrors = {};
            this.sectionDrafts = {};
            Object.keys(this.dirtyState).forEach(sectionId => {
                this.dirtyState[sectionId] = false;
            });
            if (this.currentSection && this.currentSectionType !== 'custom') {
                await this.loadSection(this.currentSection);
            } else {
                this.syncTopBar();
            }
        }

        initModelWidgets() {
            this.$formContent.find('.js-model-chain').each((_, el) => {
                const fieldPath = $(el).data('field-path');
                this.ensureModelMetaForField(fieldPath);
                this.renderModelChain(fieldPath);
            });
        }

        ensureModelMetaForField(fieldPath) {
            const models = getByPath(this.formData, fieldPath) || [];
            models.forEach(modelRef => this.loadModelMeta(modelRef));
        }

        async loadModelMeta(modelRef) {
            if (this.mcMeta[modelRef] || this.mcLoading[modelRef]) return;
            const parts = String(modelRef).split('/');
            if (parts.length < 2) return;
            const alias = parts[0];
            const modelID = parts.slice(1).join('/');
            this.mcLoading[modelRef] = true;
            try {
                const resp = await fetch(`/setup/api/models/${encodeURIComponent(alias)}`);
                const data = await resp.json();
                if (data.success && Array.isArray(data.data.models)) {
                    const model = data.data.models.find(item => item.id === modelID);
                    if (model) this.mcMeta[modelRef] = model;
                }
            } catch (_err) {
                // Metadata is advisory; keep the widget usable.
            } finally {
                delete this.mcLoading[modelRef];
                this.renderAllModelChains();
            }
        }

        renderAllModelChains() {
            this.$formContent.find('.js-model-chain').each((_, el) => {
                this.renderModelChain($(el).data('field-path'));
            });
        }

        renderModelChain(fieldPath) {
            const $widget = this.$formContent.find(`.js-model-chain[data-field-path="${fieldPath}"]`);
            if (!$widget.length) return;
            const models = getByPath(this.formData, fieldPath) || [];
            let html = '<ul class="model-chain-list">';
            models.forEach((modelRef, index) => {
                const meta = this.mcMeta[modelRef];
                const expanded = !!this.mcExpanded[modelRef];
                html += `<li class="model-chain-item" draggable="true" data-mc-item="1" data-field-path="${escapeHtml(fieldPath)}" data-index="${index}">`;
                html += `<div class="model-chain-drag-handle"><i class="bi bi-grip-vertical"></i></div>`;
                html += `<div class="model-chain-content">`;
                html += `<div class="model-chain-header"><span class="model-chain-ref">${escapeHtml(modelRef)}</span>${index === 0 ? '<span class="badge bg-primary">Primary</span>' : ''}</div>`;
                if (meta) {
                    html += `<div class="model-chain-meta"><span class="model-chain-name">${escapeHtml(meta.name || meta.id || modelRef)}</span> <span>${escapeHtml(this.formatContext(meta.contextWindow))}</span> <span>${escapeHtml(this.formatCost(meta.cost))}</span></div>`;
                    html += `<div class="model-chain-caps">`;
                    html += this.renderCapabilityBadge(meta, 'vision', 'Vision');
                    html += this.renderCapabilityBadge(meta, 'toolUse', 'Tools');
                    html += this.renderCapabilityBadge(meta, 'reasoning', 'Reasoning');
                    html += `</div>`;
                    html += `<div class="model-chain-details${expanded ? '' : ' d-none'}">`;
                    html += `<dl class="row mb-0 small">`;
                    html += `<dt class="col-4">Context</dt><dd class="col-8">${escapeHtml(((meta.contextWindow || 0).toLocaleString()) + ' tokens')}</dd>`;
                    html += `<dt class="col-4">Max Output</dt><dd class="col-8">${escapeHtml(((meta.maxOutputTokens || 0).toLocaleString()) + ' tokens')}</dd>`;
                    html += `<dt class="col-4">Cost (1M)</dt><dd class="col-8">$${escapeHtml(meta.cost && meta.cost.input ? meta.cost.input.toFixed(2) : '?')} / $${escapeHtml(meta.cost && meta.cost.output ? meta.cost.output.toFixed(2) : '?')}</dd>`;
                    html += `</dl></div>`;
                } else if (this.mcLoading[modelRef]) {
                    html += `<div class="model-chain-meta text-muted"><i class="bi bi-hourglass-split"></i> Loading...</div>`;
                }
                html += `</div>`;
                html += `<div class="model-chain-actions">`;
                html += `<button type="button" class="model-chain-btn js-model-toggle" data-model-ref="${escapeHtml(modelRef)}" title="Details"><i class="bi ${expanded ? 'bi-chevron-up' : 'bi-chevron-down'}"></i></button>`;
                html += `<button type="button" class="model-chain-btn model-chain-btn-remove js-model-remove" data-field-path="${escapeHtml(fieldPath)}" data-index="${index}" title="Remove"><i class="bi bi-x-lg"></i></button>`;
                html += `</div></li>`;
            });
            html += `</ul>`;
            html += `<div class="model-chain-add js-model-chain-add" data-field-path="${escapeHtml(fieldPath)}"><i class="bi bi-plus-lg me-2"></i> Add Fallback Model</div>`;
            $widget.html(html);
        }

        renderCapabilityBadge(meta, capabilityKey, label) {
            const enabled = !!(meta.capabilities && meta.capabilities[capabilityKey]);
            return `<span class="model-chain-cap ${enabled ? 'model-chain-cap-ok' : 'model-chain-cap-warn'}"><i class="bi ${enabled ? 'bi-check' : 'bi-x'}"></i> ${escapeHtml(label)}</span>`;
        }

        formatContext(ctx) {
            if (!ctx) return '';
            return `${Math.round(ctx / 1000)}k ctx`;
        }

        formatCost(cost) {
            if (!cost) return '';
            return `$${cost.input != null ? cost.input.toFixed(2) : '?'} / $${cost.output != null ? cost.output.toFixed(2) : '?'}`;
        }

        toggleModelExpanded(modelRef) {
            this.mcExpanded[modelRef] = !this.mcExpanded[modelRef];
            this.renderAllModelChains();
        }

        removeModel(fieldPath, index) {
            const models = [...(getByPath(this.formData, fieldPath) || [])];
            models.splice(index, 1);
            setByPath(this.formData, fieldPath, models);
            this.markCurrentSectionDirty();
            this.renderModelChain(fieldPath);
        }

        handleModelDragStart(event) {
            const $item = $(event.currentTarget);
            this.mcDrag = {
                fieldPath: $item.data('field-path'),
                index: Number($item.data('index'))
            };
            event.originalEvent.dataTransfer.effectAllowed = 'move';
            event.originalEvent.dataTransfer.setData('text/plain', String(this.mcDrag.index));
            $item.addClass('mc-dragging');
        }

        handleModelDragOver(event) {
            event.preventDefault();
            if (event.originalEvent && event.originalEvent.dataTransfer) {
                event.originalEvent.dataTransfer.dropEffect = 'move';
            }
        }

        handleModelDrop(event) {
            event.preventDefault();
            const $target = $(event.currentTarget);
            $target.removeClass('mc-drag-over');
            if (!this.mcDrag) return;

            const fieldPath = $target.data('field-path');
            if (fieldPath !== this.mcDrag.fieldPath) {
                this.clearModelDragState();
                return;
            }

            const models = [...(getByPath(this.formData, fieldPath) || [])];
            let targetIndex = Number($target.data('index'));
            if ($target.hasClass('js-model-chain-add')) {
                targetIndex = models.length;
            }
            if (Number.isNaN(targetIndex) || targetIndex === this.mcDrag.index) {
                this.clearModelDragState();
                return;
            }

            const moved = models.splice(this.mcDrag.index, 1)[0];
            if (targetIndex > this.mcDrag.index) {
                targetIndex -= 1;
            }
            models.splice(targetIndex, 0, moved);
            setByPath(this.formData, fieldPath, models);
            this.markCurrentSectionDirty();
            this.clearModelDragState();
            this.renderModelChain(fieldPath);
        }

        clearModelDragState() {
            this.mcDrag = null;
            this.$formContent.find('.mc-dragging').removeClass('mc-dragging');
            this.$formContent.find('.mc-drag-over').removeClass('mc-drag-over');
        }

        async ensureProvidersLoaded() {
            if (this.mcProvidersLoaded) return;
            try {
                const resp = await fetch('/setup/api/providers');
                const data = await resp.json();
                if (data.success) {
                    this.mcProviders = data.data.providers || [];
                    this.mcProvidersLoaded = true;
                }
            } catch (_err) {
                this.mcProviders = [];
            }
        }

        async openModelModal(fieldPath) {
            this.mcModalField = fieldPath;
            this.mcModalStep = 1;
            this.mcSelectedProvider = null;
            this.mcSelectedModel = null;
            this.mcAvailableModels = [];
            this.mcModelSearch = '';
            $('#mcModelSearch').val('');
            await this.ensureProvidersLoaded();
            this.renderModelModal();
            this.mcModal.show();
        }

        renderModelModal() {
            $('#mcModalTitle').text(this.mcModalStep === 1 ? 'Select Provider' : 'Select Model');
            $('#mcModalStepProviders').toggleClass('d-none', this.mcModalStep !== 1);
            $('#mcModalStepModels').toggleClass('d-none', this.mcModalStep !== 2);
            $('#mcModalBack').toggleClass('d-none', this.mcModalStep !== 2);
            $('#mcModalAdd').toggleClass('d-none', this.mcModalStep !== 2).prop('disabled', !this.mcSelectedModel);

            if (this.mcModalStep === 1) {
                const items = this.mcProviders.map(provider => (
                    `<button type="button" class="list-group-item list-group-item-action d-flex justify-content-between align-items-center js-model-provider-select" data-provider-alias="${escapeHtml(provider.alias)}">` +
                    `<div><div class="fw-bold">${escapeHtml(provider.name || provider.alias)}</div><small class="text-muted">${escapeHtml(provider.driver + (provider.modelCount ? ' · ' + provider.modelCount + ' models' : ''))}</small></div>` +
                    `<i class="bi bi-chevron-right"></i></button>`
                )).join('');
                $('#mcProviderList').html(items);
                $('#mcProviderEmpty').toggleClass('d-none', this.mcProviders.length !== 0);
                return;
            }

            const filtered = this.mcAvailableModels.filter(model => {
                if (!this.mcModelSearch) return true;
                const search = this.mcModelSearch.toLowerCase();
                return (model.id || '').toLowerCase().includes(search) || (model.name || '').toLowerCase().includes(search);
            });

            const items = filtered.map(model => {
                const active = this.mcSelectedModel && this.mcSelectedModel.id === model.id;
                return `<button type="button" class="model-selector-item js-model-select${active ? ' active' : ''}" data-model-id="${escapeHtml(model.id)}">` +
                    `<div class="d-flex align-items-center"><span>${escapeHtml(model.name || model.id)}</span>${model.isDefault ? '<span class="badge bg-success ms-2">default</span>' : ''}</div>` +
                    `<small class="text-muted d-block${active ? ' text-white-50' : ''}">${escapeHtml(model.id)}</small></button>`;
            }).join('');

            $('#mcModelList').html(items);
            $('#mcModelEmpty').toggleClass('d-none', filtered.length !== 0 || this.mcAvailableModels.length === 0);
            $('#mcModelLoading').toggleClass('d-none', true);
            $('#mcModelDetails').html(this.renderModelDetails(this.mcSelectedModel));
        }

        renderModelDetails(model) {
            if (!model) {
                return '<div class="text-center text-muted py-5">Select a model to see details</div>';
            }
            return `<div>
                <h6 class="mb-3">${escapeHtml(model.name || model.id)}</h6>
                <dl class="row small">
                    <dt class="col-5">Context</dt><dd class="col-7">${escapeHtml(((model.contextWindow || 0).toLocaleString()) + ' tokens')}</dd>
                    <dt class="col-5">Max Output</dt><dd class="col-7">${escapeHtml(((model.maxOutputTokens || 0).toLocaleString()) + ' tokens')}</dd>
                    <dt class="col-5">Cost (1M tokens)</dt><dd class="col-7">$${escapeHtml(model.cost && model.cost.input ? model.cost.input.toFixed(2) : '?')} in / $${escapeHtml(model.cost && model.cost.output ? model.cost.output.toFixed(2) : '?')} out</dd>
                </dl>
                <h6 class="mt-3 mb-2">Capabilities</h6>
                <div class="d-flex flex-wrap gap-1">
                    ${this.renderCapabilityPill(model, 'vision', 'Vision')}
                    ${this.renderCapabilityPill(model, 'toolUse', 'Tool Use')}
                    ${this.renderCapabilityPill(model, 'reasoning', 'Reasoning')}
                    ${this.renderCapabilityPill(model, 'structuredOutput', 'Structured')}
                </div>
                ${model.knowledgeCutoff ? `<div class="mt-3"><small class="text-muted">Knowledge cutoff: ${escapeHtml(model.knowledgeCutoff)}</small></div>` : ''}
            </div>`;
        }

        renderCapabilityPill(model, key, label) {
            const enabled = !!(model.capabilities && model.capabilities[key]);
            return `<span class="badge ${enabled ? 'bg-success' : 'bg-secondary'}"><i class="bi ${enabled ? 'bi-check' : 'bi-x'}"></i> ${escapeHtml(label)}</span>`;
        }

        showModelProviderStep() {
            this.mcModalStep = 1;
            this.mcSelectedModel = null;
            this.renderModelModal();
        }

        async selectModelProvider(alias) {
            const provider = this.mcProviders.find(item => item.alias === alias);
            if (!provider) return;
            this.mcSelectedProvider = provider;
            this.mcSelectedModel = null;
            this.mcModalStep = 2;
            $('#mcModelLoading').removeClass('d-none');
            $('#mcModelEmpty').addClass('d-none');
            $('#mcModelList').empty();
            $('#mcModelDetails').html('<div class="text-center text-muted py-5">Select a model to see details</div>');
            this.renderModelModal();

            try {
                const resp = await fetch(`/setup/api/models/${encodeURIComponent(alias)}`);
                const data = await resp.json();
                if (data.success) {
                    this.mcAvailableModels = data.data.models || [];
                    this.mcSelectedModel = this.mcAvailableModels.find(model => model.isDefault) || this.mcAvailableModels[0] || null;
                }
            } catch (_err) {
                this.mcAvailableModels = [];
            } finally {
                $('#mcModelLoading').addClass('d-none');
                this.renderModelModal();
            }
        }

        selectModelByID(modelID) {
            this.mcSelectedModel = this.mcAvailableModels.find(item => item.id === modelID) || null;
            this.renderModelModal();
        }

        addSelectedModelToChain() {
            if (!this.mcSelectedProvider || !this.mcSelectedModel || !this.mcModalField) return;
            const modelRef = `${this.mcSelectedProvider.alias}/${this.mcSelectedModel.id}`;
            const models = [...(getByPath(this.formData, this.mcModalField) || [])];
            if (models.includes(modelRef)) {
                window.alert('This model is already in the chain.');
                return;
            }
            models.push(modelRef);
            setByPath(this.formData, this.mcModalField, models);
            this.mcMeta[modelRef] = this.mcSelectedModel;
            this.markCurrentSectionDirty();
            this.renderModelChain(this.mcModalField);
            this.mcModal.hide();
        }

        getProviderUi(fieldPath) {
            if (!this.providerUi[fieldPath]) {
                this.providerUi[fieldPath] = {
                    expanded: {},
                    showKey: {},
                    adding: false,
                    presetFilter: '',
                    newPresetID: '',
                    newAlias: '',
                    newConfig: {}
                };
            }
            return this.providerUi[fieldPath];
        }

        async ensureProviderPresets() {
            if (this.providerPresetsLoaded) return;
            try {
                const resp = await fetch('/setup/api/presets');
                const data = await resp.json();
                if (data.success) {
                    this.providerPresets = data.data.presets || [];
                    this.providerPresetsLoaded = true;
                }
            } catch (_err) {
                this.providerPresets = [];
            }
        }

        renderProviderLists() {
            this.ensureProviderPresets().then(() => {
                this.$formContent.find('.js-provider-list').each((_, el) => {
                    this.renderProviderList($(el).data('field-path'));
                });
            });
        }

        renderProviderList(fieldPath) {
            const $widget = this.$formContent.find(`.js-provider-list[data-field-path="${fieldPath}"]`);
            if (!$widget.length) return;
            const ui = this.getProviderUi(fieldPath);
            const providers = deepClone(getByPath(this.formData, fieldPath) || {});
            const aliases = Object.keys(providers).sort();
            let html = '<div class="provider-list">';

            aliases.forEach(alias => {
                const cfg = providers[alias] || {};
                const expanded = !!ui.expanded[alias];
                const showKey = !!ui.showKey[alias];
                const baseURL = cfg.baseURL || cfg.url || '';
                html += `<div class="provider-item${expanded ? ' provider-item-expanded' : ''}">`;
                html += `<div class="provider-header"><div class="provider-info"><span class="provider-alias">${escapeHtml(alias)}</span>`;
                html += `<span class="provider-meta"><span>${escapeHtml(this.providerPresetName(cfg))}</span><span class="provider-key">${escapeHtml(this.maskApiKey(cfg.apiKey))}</span>${cfg.promptCaching ? '<span class="badge bg-info">Cache</span>' : ''}</span></div>`;
                html += `<div class="provider-actions"><button type="button" class="provider-btn js-provider-toggle" data-field-path="${escapeHtml(fieldPath)}" data-alias="${escapeHtml(alias)}"><i class="bi ${expanded ? 'bi-chevron-up' : 'bi-chevron-down'}"></i></button>`;
                html += `<button type="button" class="provider-btn provider-btn-remove js-provider-delete" data-field-path="${escapeHtml(fieldPath)}" data-alias="${escapeHtml(alias)}"><i class="bi bi-x-lg"></i></button></div></div>`;
                if (expanded) {
                    html += `<div class="provider-form"><div class="row g-3">`;
                    html += `<div class="col-md-6"><label class="form-label">Alias</label><input type="text" class="form-control form-control-sm" value="${escapeHtml(alias)}" disabled></div>`;
                    html += `<div class="col-md-6"><label class="form-label">Driver</label><select class="form-select form-select-sm js-provider-input" data-field-path="${escapeHtml(fieldPath)}" data-alias="${escapeHtml(alias)}" data-provider-field="driver">${this.renderOptions(PROVIDER_DRIVER_OPTIONS, cfg.driver || '')}</select></div>`;
                    html += `<div class="col-12"><label class="form-label">API Key</label><div class="input-group input-group-sm"><input type="${showKey ? 'text' : 'password'}" class="form-control js-provider-input" data-field-path="${escapeHtml(fieldPath)}" data-alias="${escapeHtml(alias)}" data-provider-field="apiKey" value="${escapeHtml(cfg.apiKey || '')}" placeholder="Enter API key"><button type="button" class="btn btn-outline-secondary js-provider-toggle-key" data-field-path="${escapeHtml(fieldPath)}" data-alias="${escapeHtml(alias)}"><i class="bi ${showKey ? 'bi-eye-slash' : 'bi-eye'}"></i></button></div></div>`;
                    html += `<div class="col-12"><label class="form-label">Base URL (optional)</label><input type="text" class="form-control form-control-sm js-provider-input" data-field-path="${escapeHtml(fieldPath)}" data-alias="${escapeHtml(alias)}" data-provider-field="baseURL" value="${escapeHtml(baseURL)}" placeholder="Leave empty for default"></div>`;
                    html += `<div class="col-md-6"><div class="form-check form-switch"><input class="form-check-input js-provider-input" type="checkbox" data-field-path="${escapeHtml(fieldPath)}" data-alias="${escapeHtml(alias)}" data-provider-field="promptCaching"${cfg.promptCaching ? ' checked' : ''}><label class="form-check-label">Prompt Caching</label></div></div>`;
                    html += `<div class="col-md-6"><label class="form-label">Thinking Level</label><select class="form-select form-select-sm js-provider-input" data-field-path="${escapeHtml(fieldPath)}" data-alias="${escapeHtml(alias)}" data-provider-field="thinkingLevel">${this.renderOptions(THINKING_LEVEL_OPTIONS, cfg.thinkingLevel || '')}</select></div>`;
                    html += `<div class="col-md-6"><label class="form-label">Max Tokens</label><input type="number" class="form-control form-control-sm js-provider-input" data-field-path="${escapeHtml(fieldPath)}" data-alias="${escapeHtml(alias)}" data-provider-field="maxTokens" value="${escapeHtml(cfg.maxTokens || '')}" placeholder="0 = default"></div>`;
                    html += `<div class="col-md-6"><label class="form-label">Timeout (seconds)</label><input type="number" class="form-control form-control-sm js-provider-input" data-field-path="${escapeHtml(fieldPath)}" data-alias="${escapeHtml(alias)}" data-provider-field="timeoutSeconds" value="${escapeHtml(cfg.timeoutSeconds || '')}" placeholder="0 = default"></div>`;
                    html += `</div></div>`;
                }
                html += `</div>`;
            });

            if (ui.adding) {
                const preset = this.providerPresets.find(item => item.id === ui.newPresetID) || null;
                const filtered = this.providerPresets.filter(item => {
                    const search = (ui.presetFilter || '').toLowerCase();
                    if (!search) return true;
                    return item.name.toLowerCase().includes(search) || item.id.toLowerCase().includes(search);
                });
                html += `<div class="provider-item provider-item-new"><div class="provider-header"><div class="provider-info"><span class="provider-alias">New Provider</span></div><div class="provider-actions"><button type="button" class="provider-btn provider-btn-remove js-provider-add-cancel" data-field-path="${escapeHtml(fieldPath)}"><i class="bi bi-x-lg"></i></button></div></div>`;
                html += `<div class="provider-add-form"><div class="row g-0"><div class="col-md-5 provider-preset-list"><div class="p-2 border-bottom"><input type="text" class="form-control form-control-sm js-provider-preset-filter" data-field-path="${escapeHtml(fieldPath)}" value="${escapeHtml(ui.presetFilter)}" placeholder="Filter presets..."></div><div class="provider-preset-items">`;
                filtered.forEach(item => {
                    const active = ui.newPresetID === item.id;
                    html += `<button type="button" class="provider-preset-item js-provider-preset-select${active ? ' active' : ''}" data-field-path="${escapeHtml(fieldPath)}" data-preset-id="${escapeHtml(item.id)}"><div class="provider-preset-name">${escapeHtml(item.name)}</div><div class="provider-preset-meta"><span>${escapeHtml(item.driver)}</span>${item.isLocal ? '<span class="badge bg-secondary">local</span>' : ''}${item.modelCount ? `<span>${escapeHtml(item.modelCount + ' models')}</span>` : ''}</div></button>`;
                });
                html += `</div></div><div class="col-md-7 provider-new-config">`;
                if (preset) {
                    const cfg = ui.newConfig || {};
                    html += `<div class="p-3"><div class="mb-3"><label class="form-label">Alias *</label><input type="text" class="form-control form-control-sm js-provider-input" data-field-path="${escapeHtml(fieldPath)}" data-provider-field="newAlias" value="${escapeHtml(ui.newAlias)}" placeholder="e.g., anthropic"><div class="form-text">A unique name to identify this provider</div></div>`;
                    html += `<div class="mb-3"><label class="form-label">API Key *</label><input type="password" class="form-control form-control-sm js-provider-input" data-field-path="${escapeHtml(fieldPath)}" data-provider-field="newApiKey" value="${escapeHtml(cfg.apiKey || '')}" placeholder="Enter API key"></div>`;
                    if (preset.driver === 'anthropic') {
                        html += `<div class="mb-3"><div class="form-check form-switch"><input class="form-check-input js-provider-input" type="checkbox" data-field-path="${escapeHtml(fieldPath)}" data-provider-field="newPromptCaching"${cfg.promptCaching ? ' checked' : ''}><label class="form-check-label">Enable Prompt Caching</label></div></div>`;
                    }
                    html += `<button type="button" class="btn btn-primary btn-sm js-provider-save-new" data-field-path="${escapeHtml(fieldPath)}"${!ui.newAlias || !cfg.apiKey ? ' disabled' : ''}><i class="bi bi-plus-lg me-1"></i> Add Provider</button></div>`;
                } else {
                    html += `<div class="p-4 text-center text-muted">Select a provider preset from the list</div>`;
                }
                html += `</div></div></div></div>`;
            } else {
                html += `<div class="provider-add js-provider-add-start" data-field-path="${escapeHtml(fieldPath)}"><i class="bi bi-plus-lg me-2"></i> Add Provider</div>`;
            }

            html += '</div>';
            $widget.html(html);
        }

        renderOptions(options, selected) {
            return options.map(opt => `<option value="${escapeHtml(opt.value)}"${opt.value === selected ? ' selected' : ''}>${escapeHtml(opt.label)}</option>`).join('');
        }

        providerPresetName(cfg) {
            if (!cfg) return 'Unknown';
            const preset = this.providerPresets.find(item => item.id === cfg.subtype || item.driver === cfg.driver);
            return preset ? preset.name : (cfg.subtype || cfg.driver || 'Unknown');
        }

        maskApiKey(key) {
            if (!key) return '(not set)';
            if (key.length <= 8) return '••••••••';
            return `${key.substring(0, 6)}...${key.substring(key.length - 3)}`;
        }

        toggleProvider(fieldPath, alias) {
            const ui = this.getProviderUi(fieldPath);
            ui.expanded[alias] = !ui.expanded[alias];
            this.renderProviderList(fieldPath);
        }

        toggleProviderKey(fieldPath, alias) {
            const ui = this.getProviderUi(fieldPath);
            ui.showKey[alias] = !ui.showKey[alias];
            this.renderProviderList(fieldPath);
        }

        startAddProvider(fieldPath) {
            const ui = this.getProviderUi(fieldPath);
            ui.adding = true;
            ui.newPresetID = '';
            ui.newAlias = '';
            ui.newConfig = {};
            ui.presetFilter = '';
            this.renderProviderList(fieldPath);
        }

        cancelAddProvider(fieldPath) {
            const ui = this.getProviderUi(fieldPath);
            ui.adding = false;
            ui.newPresetID = '';
            ui.newAlias = '';
            ui.newConfig = {};
            ui.presetFilter = '';
            this.renderProviderList(fieldPath);
        }

        updateProviderPresetFilter(fieldPath, value) {
            const ui = this.getProviderUi(fieldPath);
            ui.presetFilter = value || '';
            this.renderProviderList(fieldPath);
        }

        selectProviderPreset(fieldPath, presetID) {
            const ui = this.getProviderUi(fieldPath);
            const preset = this.providerPresets.find(item => item.id === presetID);
            if (!preset) return;
            ui.newPresetID = preset.id;
            ui.newAlias = preset.id;
            ui.newConfig = {
                driver: preset.driver,
                subtype: preset.id,
                apiKey: '',
                baseURL: preset.driver === 'ollama' ? '' : (preset.apiEndpoint || ''),
                url: preset.driver === 'ollama' ? (preset.apiEndpoint || '') : '',
                promptCaching: preset.driver === 'anthropic'
            };
            this.renderProviderList(fieldPath);
        }

        handleProviderInput(event) {
            const $input = $(event.currentTarget);
            const fieldPath = $input.data('field-path');
            const alias = $input.data('alias');
            const providerField = $input.data('provider-field');
            const ui = this.getProviderUi(fieldPath);
            const providers = deepClone(getByPath(this.formData, fieldPath) || {});

            if (!providerField) return;
            if (providerField === 'newAlias') {
                ui.newAlias = ($input.val() || '').trim();
                this.renderProviderList(fieldPath);
                return;
            }
            if (providerField === 'newApiKey') {
                ui.newConfig.apiKey = $input.val() || '';
                this.renderProviderList(fieldPath);
                return;
            }
            if (providerField === 'newPromptCaching') {
                ui.newConfig.promptCaching = $input.is(':checked');
                this.renderProviderList(fieldPath);
                return;
            }

            if (!alias || !providers[alias]) return;
            let value = $input.is(':checkbox') ? $input.is(':checked') : $input.val();
            if (providerField === 'maxTokens' || providerField === 'timeoutSeconds') {
                value = value === '' ? 0 : Number(value);
            }
            if (providerField === 'baseURL') {
                if (providers[alias].driver === 'ollama') {
                    providers[alias].url = value;
                } else {
                    providers[alias].baseURL = value;
                }
            } else if (providerField === 'driver') {
                providers[alias].driver = value;
                if (value === 'ollama') {
                    providers[alias].url = providers[alias].url || providers[alias].baseURL || '';
                    delete providers[alias].baseURL;
                }
            } else {
                providers[alias][providerField] = value;
            }
            setByPath(this.formData, fieldPath, providers);
            this.markCurrentSectionDirty();
            this.mcProvidersLoaded = false;
            this.renderProviderList(fieldPath);
        }

        saveNewProvider(fieldPath) {
            const ui = this.getProviderUi(fieldPath);
            const preset = this.providerPresets.find(item => item.id === ui.newPresetID);
            if (!preset || !ui.newAlias) return;

            const providers = deepClone(getByPath(this.formData, fieldPath) || {});
            if (providers[ui.newAlias]) {
                window.alert('A provider with this alias already exists.');
                return;
            }

            const cfg = deepClone(ui.newConfig || {});
            if (cfg.driver === 'ollama') {
                cfg.url = cfg.url || cfg.baseURL || '';
                delete cfg.baseURL;
            }

            providers[ui.newAlias] = cfg;
            setByPath(this.formData, fieldPath, providers);
            ui.expanded[ui.newAlias] = true;
            this.markCurrentSectionDirty();
            this.mcProvidersLoaded = false;
            this.cancelAddProvider(fieldPath);
        }

        deleteProvider(fieldPath, alias) {
            if (!window.confirm(`Delete provider "${alias}"? This cannot be undone.`)) return;
            const providers = deepClone(getByPath(this.formData, fieldPath) || {});
            delete providers[alias];
            setByPath(this.formData, fieldPath, providers);
            this.markCurrentSectionDirty();
            this.mcProvidersLoaded = false;
            this.renderProviderList(fieldPath);
        }

        getRoleUi(fieldPath) {
            if (!this.rolesUi[fieldPath]) {
                this.rolesUi[fieldPath] = {
                    expanded: {},
                    adding: false,
                    newName: '',
                    copyFrom: '',
                    newConfig: this.defaultRoleConfig()
                };
            }
            return this.rolesUi[fieldPath];
        }

        defaultRoleConfig() {
            return {
                tools: '*',
                skills: '*',
                memory: 'none',
                transcripts: 'none',
                commands: false,
                systemPrompt: '',
                systemPromptFile: ''
            };
        }

        renderRolesLists() {
            this.$formContent.find('.js-roles-list').each((_, el) => {
                this.renderRolesList($(el).data('field-path'));
            });
        }

        renderRolesList(fieldPath) {
            const $widget = this.$formContent.find(`.js-roles-list[data-field-path="${fieldPath}"]`);
            if (!$widget.length) return;
            const roles = deepClone(getByPath(this.formData, fieldPath) || {});
            const names = Object.keys(roles).sort();
            const ui = this.getRoleUi(fieldPath);
            let html = '<div class="role-list">';

            names.forEach(name => {
                const role = roles[name] || {};
                const expanded = !!ui.expanded[name];
                html += `<div class="role-item${expanded ? ' role-item-expanded' : ''}"><div class="role-header"><div class="role-info"><span class="role-name">${escapeHtml(name)}</span><span class="role-summary">${escapeHtml(this.roleSummary(role))}</span></div>`;
                html += `<div class="role-actions"><button type="button" class="role-btn js-role-toggle" data-field-path="${escapeHtml(fieldPath)}" data-role-name="${escapeHtml(name)}"><i class="bi ${expanded ? 'bi-chevron-up' : 'bi-chevron-down'}"></i></button>`;
                html += `<button type="button" class="role-btn role-btn-remove js-role-delete" data-field-path="${escapeHtml(fieldPath)}" data-role-name="${escapeHtml(name)}"><i class="bi bi-x-lg"></i></button></div></div>`;
                if (expanded) {
                    html += `<div class="role-form"><div class="row g-3">`;
                    html += this.renderRoleListEditor(fieldPath, name, 'tools', role.tools, 'Tools');
                    html += this.renderRoleListEditor(fieldPath, name, 'skills', role.skills, 'Skills');
                    html += `<div class="col-md-6"><label class="form-label">Memory Access</label><select class="form-select form-select-sm js-role-input" data-field-path="${escapeHtml(fieldPath)}" data-role-name="${escapeHtml(name)}" data-role-field="memory">${this.renderOptions(MEMORY_OPTIONS, role.memory || 'none')}</select></div>`;
                    html += `<div class="col-md-6"><label class="form-label">Transcript Access</label><select class="form-select form-select-sm js-role-input" data-field-path="${escapeHtml(fieldPath)}" data-role-name="${escapeHtml(name)}" data-role-field="transcripts">${this.renderOptions(TRANSCRIPT_OPTIONS, role.transcripts || 'none')}</select></div>`;
                    html += `<div class="col-12"><div class="form-check form-switch"><input class="form-check-input js-role-input" type="checkbox" data-field-path="${escapeHtml(fieldPath)}" data-role-name="${escapeHtml(name)}" data-role-field="commands"${role.commands ? ' checked' : ''}><label class="form-check-label">Enable Slash Commands</label></div></div>`;
                    html += `<div class="col-12"><label class="form-label">System Prompt File (optional)</label><input type="text" class="form-control form-control-sm js-role-input" data-field-path="${escapeHtml(fieldPath)}" data-role-name="${escapeHtml(name)}" data-role-field="systemPromptFile" value="${escapeHtml(role.systemPromptFile || '')}" placeholder="prompts/role.md"><div class="form-text">Path to prompt file (relative to workspace)</div></div>`;
                    html += `</div></div>`;
                }
                html += `</div>`;
            });

            if (ui.adding) {
                html += `<div class="role-item role-item-new"><div class="role-header"><div class="role-info"><span class="role-name">New Role</span></div><div class="role-actions"><button type="button" class="role-btn role-btn-remove js-role-add-cancel" data-field-path="${escapeHtml(fieldPath)}"><i class="bi bi-x-lg"></i></button></div></div>`;
                html += `<div class="role-form"><div class="row g-3">`;
                html += `<div class="col-md-6"><label class="form-label">Role Name *</label><input type="text" class="form-control form-control-sm js-role-input" data-field-path="${escapeHtml(fieldPath)}" data-role-field="newName" value="${escapeHtml(ui.newName)}" placeholder="e.g., assistant"></div>`;
                html += `<div class="col-md-6"><label class="form-label">Copy permissions from</label><select class="form-select form-select-sm js-role-input" data-field-path="${escapeHtml(fieldPath)}" data-role-field="copyFrom"><option value="">(start fresh)</option>${names.map(name => `<option value="${escapeHtml(name)}"${ui.copyFrom === name ? ' selected' : ''}>${escapeHtml(name)}</option>`).join('')}</select></div>`;
                html += this.renderRoleListEditor(fieldPath, '__new__', 'tools', ui.newConfig.tools, 'Tools');
                html += this.renderRoleListEditor(fieldPath, '__new__', 'skills', ui.newConfig.skills, 'Skills');
                html += `<div class="col-md-6"><label class="form-label">Memory Access</label><select class="form-select form-select-sm js-role-input" data-field-path="${escapeHtml(fieldPath)}" data-role-name="__new__" data-role-field="memory">${this.renderOptions(MEMORY_OPTIONS, ui.newConfig.memory || 'none')}</select></div>`;
                html += `<div class="col-md-6"><label class="form-label">Transcript Access</label><select class="form-select form-select-sm js-role-input" data-field-path="${escapeHtml(fieldPath)}" data-role-name="__new__" data-role-field="transcripts">${this.renderOptions(TRANSCRIPT_OPTIONS, ui.newConfig.transcripts || 'none')}</select></div>`;
                html += `<div class="col-12"><div class="form-check form-switch"><input class="form-check-input js-role-input" type="checkbox" data-field-path="${escapeHtml(fieldPath)}" data-role-name="__new__" data-role-field="commands"${ui.newConfig.commands ? ' checked' : ''}><label class="form-check-label">Enable Slash Commands</label></div></div>`;
                html += `<div class="col-12"><label class="form-label">System Prompt File (optional)</label><input type="text" class="form-control form-control-sm js-role-input" data-field-path="${escapeHtml(fieldPath)}" data-role-name="__new__" data-role-field="systemPromptFile" value="${escapeHtml(ui.newConfig.systemPromptFile || '')}" placeholder="prompts/role.md"></div>`;
                html += `<div class="col-12"><button type="button" class="btn btn-primary btn-sm js-role-save-new" data-field-path="${escapeHtml(fieldPath)}"${!ui.newName ? ' disabled' : ''}><i class="bi bi-plus-lg me-1"></i> Add Role</button></div>`;
                html += `</div></div></div>`;
            } else {
                html += `<div class="role-add js-role-add-start" data-field-path="${escapeHtml(fieldPath)}"><i class="bi bi-plus-lg me-2"></i> Add Role</div>`;
            }

            html += '</div>';
            $widget.html(html);
        }

        renderRoleListEditor(fieldPath, roleName, field, value, label) {
            const all = this.roleIsAll(value);
            const listValue = escapeHtml(this.roleList(value).join(', '));
            return `<div class="col-12"><label class="form-label">${escapeHtml(label)}</label>
                <div class="mb-2">
                    <div class="form-check form-check-inline"><input class="form-check-input js-role-input" type="radio" name="${escapeHtml(`${fieldPath}_${roleName}_${field}_mode`)}" data-field-path="${escapeHtml(fieldPath)}" data-role-name="${escapeHtml(roleName)}" data-role-field="${escapeHtml(field)}" data-role-mode="all"${all ? ' checked' : ''}><label class="form-check-label">All ${escapeHtml(label.toLowerCase())}</label></div>
                    <div class="form-check form-check-inline"><input class="form-check-input js-role-input" type="radio" name="${escapeHtml(`${fieldPath}_${roleName}_${field}_mode`)}" data-field-path="${escapeHtml(fieldPath)}" data-role-name="${escapeHtml(roleName)}" data-role-field="${escapeHtml(field)}" data-role-mode="specific"${!all ? ' checked' : ''}><label class="form-check-label">Specific ${escapeHtml(label.toLowerCase())}</label></div>
                </div>
                <input type="text" class="form-control form-control-sm js-role-input${all ? ' d-none' : ''}" data-field-path="${escapeHtml(fieldPath)}" data-role-name="${escapeHtml(roleName)}" data-role-field="${escapeHtml(field)}" data-role-input-type="list" value="${listValue}" placeholder="${field === 'tools' ? 'read, write, web_search, ...' : 'skill1, skill2, ...'}">
            </div>`;
        }

        roleIsAll(value) {
            return value === '*' || value === '';
        }

        roleList(value) {
            if (!value || value === '*') return [];
            if (Array.isArray(value)) return value;
            return parseStringList(value);
        }

        roleSummary(role) {
            const parts = [];
            parts.push(`${this.roleIsAll(role.tools) ? 'All' : this.roleList(role.tools).length + ' selected'} tools`);
            parts.push(`${this.roleIsAll(role.skills) ? 'All' : this.roleList(role.skills).length + ' selected'} skills`);
            parts.push(`${role.memory || 'none'} memory`);
            parts.push(`${role.transcripts || 'none'} transcripts`);
            if (role.commands) parts.push('commands');
            return parts.join(' · ');
        }

        toggleRole(fieldPath, roleName) {
            const ui = this.getRoleUi(fieldPath);
            ui.expanded[roleName] = !ui.expanded[roleName];
            this.renderRolesList(fieldPath);
        }

        startAddRole(fieldPath) {
            const ui = this.getRoleUi(fieldPath);
            ui.adding = true;
            ui.newName = '';
            ui.copyFrom = '';
            ui.newConfig = this.defaultRoleConfig();
            this.renderRolesList(fieldPath);
        }

        cancelAddRole(fieldPath) {
            const ui = this.getRoleUi(fieldPath);
            ui.adding = false;
            ui.newName = '';
            ui.copyFrom = '';
            ui.newConfig = this.defaultRoleConfig();
            this.renderRolesList(fieldPath);
        }

        handleRoleInput(event) {
            const $input = $(event.currentTarget);
            const fieldPath = $input.data('field-path');
            const roleName = $input.data('role-name');
            const roleField = $input.data('role-field');
            const roleMode = $input.data('role-mode');
            const inputType = $input.data('role-input-type');
            const roles = deepClone(getByPath(this.formData, fieldPath) || {});
            const ui = this.getRoleUi(fieldPath);

            if (roleField === 'newName') {
                ui.newName = String($input.val() || '').toLowerCase().replace(/[^a-z0-9_]/g, '_');
                this.renderRolesList(fieldPath);
                return;
            }
            if (roleField === 'copyFrom') {
                ui.copyFrom = $input.val() || '';
                if (ui.copyFrom && roles[ui.copyFrom]) {
                    ui.newConfig = deepClone(roles[ui.copyFrom]);
                } else {
                    ui.newConfig = this.defaultRoleConfig();
                }
                this.renderRolesList(fieldPath);
                return;
            }

            const target = roleName === '__new__'
                ? ui.newConfig
                : roles[roleName];
            if (!target) return;

            if (roleMode === 'all') {
                target[roleField] = '*';
            } else if (roleMode === 'specific') {
                target[roleField] = [];
            } else if (inputType === 'list') {
                target[roleField] = parseStringList($input.val());
            } else if ($input.is(':checkbox')) {
                target[roleField] = $input.is(':checked');
            } else {
                target[roleField] = $input.val();
            }

            if (roleName !== '__new__') {
                setByPath(this.formData, fieldPath, roles);
                this.markCurrentSectionDirty();
            }
            this.renderRolesList(fieldPath);
        }

        saveNewRole(fieldPath) {
            const ui = this.getRoleUi(fieldPath);
            if (!ui.newName) return;
            const roles = deepClone(getByPath(this.formData, fieldPath) || {});
            if (roles[ui.newName]) {
                window.alert('A role with this name already exists.');
                return;
            }
            roles[ui.newName] = deepClone(ui.newConfig);
            setByPath(this.formData, fieldPath, roles);
            this.markCurrentSectionDirty();
            ui.expanded[ui.newName] = true;
            this.cancelAddRole(fieldPath);
        }

        deleteRole(fieldPath, roleName) {
            if (!window.confirm(`Delete role "${roleName}"? Users with this role will need to be reassigned.`)) return;
            const roles = deepClone(getByPath(this.formData, fieldPath) || {});
            delete roles[roleName];
            setByPath(this.formData, fieldPath, roles);
            this.markCurrentSectionDirty();
            this.renderRolesList(fieldPath);
        }

        markCurrentSectionDirty() {
            if (!this.currentSection || this.currentSectionType === 'custom') return;
            this.sectionDrafts[this.currentSection] = deepClone(this.formData);
            this.dirtyState[this.currentSection] = this.currentDirty();
            this.syncTopBar();
        }

        async loadUsers() {
            this.usersLoading = true;
            this.usersError = '';
            this.usersSuccess = '';
            this.renderUsersSection();
            try {
                const resp = await fetch('/setup/api/users');
                const data = await resp.json();
                if (!data.success) throw new Error(data.message || 'Failed to load users');
                this.users = data.data.users || [];
                this.userRoles = data.data.roles || ['owner', 'user', 'guest'];
            } catch (err) {
                this.usersError = err.message || 'Failed to load users';
            } finally {
                this.usersLoading = false;
                this.renderUsersSection();
            }
        }

        renderUsersSection() {
            const usersRows = this.users.map(user => {
                const badge = user.role === 'owner' ? 'bg-danger' : (user.role === 'user' ? 'bg-primary' : 'bg-secondary');
                const ownerCount = this.users.filter(item => item.role === 'owner').length;
                return `<tr>
                    <td><code>${escapeHtml(user.username)}</code></td>
                    <td>${escapeHtml(user.name)}</td>
                    <td><span class="badge ${badge}">${escapeHtml(user.role)}</span></td>
                    <td><span class="text-muted">${escapeHtml(user.telegram_id || '-')}</span></td>
                    <td><span class="text-muted">${escapeHtml(user.whatsapp_id || '-')}</span></td>
                    <td><span class="text-muted">${user.has_password ? '●●●●' : '-'}</span></td>
                    <td class="text-end">
                        <button class="btn btn-sm btn-outline-primary js-users-edit" data-username="${escapeHtml(user.username)}" title="Edit"><i class="bi bi-pencil"></i></button>
                        <button class="btn btn-sm btn-outline-danger js-users-delete-open" data-username="${escapeHtml(user.username)}" title="Delete"${user.role === 'owner' && ownerCount <= 1 ? ' disabled' : ''}><i class="bi bi-trash"></i></button>
                    </td>
                </tr>`;
            }).join('');

            const roleOptions = this.userRoles.map(role => `<option value="${escapeHtml(role)}"${this.userForm.role === role ? ' selected' : ''}>${escapeHtml(role)}</option>`).join('');
            const thinkingVisible = !!this.userForm.thinking;
            const passwordMismatch = this.userForm.password && this.userForm.password !== this.userForm.confirmPassword;

            this.$usersContent.html(`
                <div class="card mb-4">
                    <div class="card-header d-flex justify-content-between align-items-center">
                        <span>Registered Users</span>
                        <button class="btn btn-sm btn-primary js-users-add"><i class="bi bi-plus-lg"></i> Add User</button>
                    </div>
                    <div class="card-body p-0">
                        ${this.usersLoading ? '<div class="text-center py-4"><div class="spinner-border spinner-border-sm"></div></div>' : `
                        <table class="table table-hover mb-0">
                            <thead><tr><th>Username</th><th>Name</th><th>Role</th><th>Telegram</th><th>WhatsApp</th><th>Password</th><th class="text-end">Actions</th></tr></thead>
                            <tbody>${usersRows || '<tr><td colspan="7" class="text-center text-muted py-4">No users configured</td></tr>'}</tbody>
                        </table>`}
                    </div>
                </div>
                <div class="alert alert-danger alert-dismissible ${this.usersError ? '' : 'd-none'}" id="users-error-alert"><span class="js-alert-text">${escapeHtml(this.usersError)}</span><button type="button" class="btn-close" aria-label="Close"></button></div>
                <div class="alert alert-success alert-dismissible ${this.usersSuccess ? '' : 'd-none'}" id="users-success-alert"><span class="js-alert-text">${escapeHtml(this.usersSuccess)}</span><button type="button" class="btn-close" aria-label="Close"></button></div>

                <div class="modal fade" id="userModal" tabindex="-1">
                    <div class="modal-dialog">
                        <div class="modal-content">
                            <div class="modal-header">
                                <h5 class="modal-title">${this.userEditing ? 'Edit User' : 'Add User'}</h5>
                                <button type="button" class="btn-close" data-bs-dismiss="modal"></button>
                            </div>
                            <div class="modal-body">
                                <div class="mb-3">
                                    <label class="form-label">Username</label>
                                    <input type="text" class="form-control js-user-form-field${this.userFormErrors.username ? ' is-invalid' : ''}" data-user-field="username" value="${escapeHtml(this.userForm.username || '')}" ${this.userEditing ? 'disabled' : ''} required pattern="[a-z0-9_-]+">
                                    <div class="invalid-feedback">${escapeHtml(this.userFormErrors.username || '')}</div>
                                    ${!this.userEditing ? '<div class="form-text">Lowercase letters, numbers, underscores, and hyphens only</div>' : ''}
                                </div>
                                <div class="mb-3">
                                    <label class="form-label">Display Name</label>
                                    <input type="text" class="form-control js-user-form-field${this.userFormErrors.name ? ' is-invalid' : ''}" data-user-field="name" value="${escapeHtml(this.userForm.name || '')}" required>
                                    <div class="invalid-feedback">${escapeHtml(this.userFormErrors.name || '')}</div>
                                </div>
                                <div class="mb-3">
                                    <label class="form-label">Role</label>
                                    <select class="form-select js-user-form-field${this.userFormErrors.role ? ' is-invalid' : ''}" data-user-field="role">${roleOptions}</select>
                                    <div class="invalid-feedback">${escapeHtml(this.userFormErrors.role || '')}</div>
                                </div>
                                <div class="mb-3">
                                    <label class="form-label">Telegram ID</label>
                                    <input type="text" class="form-control js-user-form-field" data-user-field="telegram_id" value="${escapeHtml(this.userForm.telegram_id || '')}" placeholder="e.g., 123456789">
                                    <div class="form-text">Numeric Telegram user ID for bot access</div>
                                </div>
                                <div class="mb-3">
                                    <label class="form-label">WhatsApp ID</label>
                                    <input type="text" class="form-control js-user-form-field" data-user-field="whatsapp_id" value="${escapeHtml(this.userForm.whatsapp_id || '')}" placeholder="e.g., 15551234567">
                                    <div class="form-text">Phone number for WhatsApp bot access (digits only)</div>
                                </div>
                                <div class="mb-3">
                                    <label class="form-label">Password (optional)</label>
                                    <input type="password" class="form-control js-user-form-field" data-user-field="password" value="${escapeHtml(this.userForm.password || '')}" placeholder="${this.userEditing ? 'Leave blank to keep existing password' : 'Leave blank for no HTTP access'}">
                                    <div class="form-text">${this.userEditing ? 'Leave blank to keep existing password. Enter a new value to replace it.' : 'Password for HTTP/web interface access'}</div>
                                </div>
                                <div class="mb-3 ${this.userForm.password ? '' : 'd-none'}">
                                    <label class="form-label">Confirm Password</label>
                                    <input type="password" class="form-control js-user-form-field${(this.userFormErrors.password_confirm || passwordMismatch) ? ' is-invalid' : ''}" data-user-field="confirmPassword" value="${escapeHtml(this.userForm.confirmPassword || '')}">
                                    <div class="invalid-feedback">${escapeHtml(this.userFormErrors.password_confirm || (passwordMismatch ? 'Passwords do not match' : ''))}</div>
                                </div>
                                <div class="mb-3 form-check ${this.userEditing && this.userForm.has_password && !this.userForm.password ? '' : 'd-none'}">
                                    <input class="form-check-input js-user-form-field" type="checkbox" data-user-field="clear_password"${this.userForm.clear_password ? ' checked' : ''} id="clearPassword">
                                    <label class="form-check-label" for="clearPassword">Remove existing password</label>
                                </div>
                                <div class="border-top pt-3 mt-3">
                                    <p class="text-muted small mb-2">Advanced Options</p>
                                    <div class="form-check mb-2">
                                        <input class="form-check-input js-user-form-field" type="checkbox" data-user-field="thinking"${this.userForm.thinking ? ' checked' : ''} id="userThinking">
                                        <label class="form-check-label" for="userThinking">Enable Extended Thinking</label>
                                    </div>
                                    <div class="mb-3 ${thinkingVisible ? '' : 'd-none'}">
                                        <label class="form-label">Thinking Level</label>
                                        <select class="form-select js-user-form-field" data-user-field="thinking_level">
                                            <option value=""${!this.userForm.thinking_level ? ' selected' : ''}>Default</option>
                                            <option value="low"${this.userForm.thinking_level === 'low' ? ' selected' : ''}>Low</option>
                                            <option value="medium"${this.userForm.thinking_level === 'medium' ? ' selected' : ''}>Medium</option>
                                            <option value="high"${this.userForm.thinking_level === 'high' ? ' selected' : ''}>High</option>
                                        </select>
                                    </div>
                                    <div class="form-check">
                                        <input class="form-check-input js-user-form-field" type="checkbox" data-user-field="sandbox"${this.userForm.sandbox ? ' checked' : ''} id="userSandbox">
                                        <label class="form-check-label" for="userSandbox">Sandbox Mode (restricted tools)</label>
                                    </div>
                                </div>
                            </div>
                            <div class="modal-footer">
                                <button type="button" class="btn btn-secondary" data-bs-dismiss="modal">Cancel</button>
                                <button type="button" class="btn btn-primary js-users-save"${passwordMismatch ? ' disabled' : ''}>${this.userEditing ? 'Update' : 'Create'}</button>
                            </div>
                        </div>
                    </div>
                </div>

                <div class="modal fade" id="deleteModal" tabindex="-1">
                    <div class="modal-dialog">
                        <div class="modal-content">
                            <div class="modal-header">
                                <h5 class="modal-title">Delete User</h5>
                                <button type="button" class="btn-close" data-bs-dismiss="modal"></button>
                            </div>
                            <div class="modal-body">
                                <p>Are you sure you want to delete user <code>${escapeHtml(this.userDeleteUsername || '')}</code>?</p>
                                <p class="text-danger small">This action cannot be undone.</p>
                            </div>
                            <div class="modal-footer">
                                <button type="button" class="btn btn-secondary" data-bs-dismiss="modal">Cancel</button>
                                <button type="button" class="btn btn-danger js-users-delete-confirm"${this.userDeleting ? ' disabled' : ''}>Delete</button>
                            </div>
                        </div>
                    </div>
                </div>
            `);

            this.$usersContent.find('#users-error-alert .btn-close').on('click', () => {
                this.usersError = '';
                this.renderUsersSection();
            });
            this.$usersContent.find('#users-success-alert .btn-close').on('click', () => {
                this.usersSuccess = '';
                this.renderUsersSection();
            });

            this.$usersContent.off('click.users');
            this.$usersContent.off('input.users change.users');
            this.$usersContent.on('click.users', '.js-users-add', () => this.openUserModal(false));
            this.$usersContent.on('click.users', '.js-users-edit', (event) => this.openUserModal(true, $(event.currentTarget).data('username')));
            this.$usersContent.on('click.users', '.js-users-delete-open', (event) => this.openDeleteUserModal($(event.currentTarget).data('username')));
            this.$usersContent.on('click.users', '.js-users-save', () => this.saveUser());
            this.$usersContent.on('click.users', '.js-users-delete-confirm', () => this.deleteUser());
            this.$usersContent.on('input.users change.users', '.js-user-form-field', (event) => this.handleUserFormInput(event));
        }

        openUserModal(editing, username) {
            this.userEditing = editing;
            this.userFormErrors = {};
            if (editing) {
                const user = this.users.find(item => item.username === username);
                if (!user) return;
                this.userForm = {
                    username: user.username,
                    name: user.name,
                    role: user.role,
                    telegram_id: user.telegram_id || '',
                    whatsapp_id: user.whatsapp_id || '',
                    password: '',
                    confirmPassword: '',
                    clear_password: false,
                    has_password: !!user.has_password,
                    thinking: !!user.thinking,
                    thinking_level: user.thinking_level || '',
                    sandbox: !!user.sandbox
                };
            } else {
                this.userForm = {
                    username: '',
                    name: '',
                    role: 'user',
                    telegram_id: '',
                    whatsapp_id: '',
                    password: '',
                    confirmPassword: '',
                    clear_password: false,
                    has_password: false,
                    thinking: false,
                    thinking_level: '',
                    sandbox: false
                };
            }
            this.renderUsersSection();
            this.userModal = new bootstrap.Modal(document.getElementById('userModal'));
            this.userModal.show();
        }

        openDeleteUserModal(username) {
            this.userDeleteUsername = username;
            this.renderUsersSection();
            this.userDeleteModal = new bootstrap.Modal(document.getElementById('deleteModal'));
            this.userDeleteModal.show();
        }

        handleUserFormInput(event) {
            const $input = $(event.currentTarget);
            const field = $input.data('user-field');
            if (!field) return;
            let value = $input.is(':checkbox') ? $input.is(':checked') : $input.val();
            this.userForm[field] = value;
            this.renderUsersSection();
            if (this.userModal) this.userModal.show();
        }

        async saveUser() {
            this.userFormErrors = {};
            this.usersError = '';
            this.usersSuccess = '';

            if (this.userForm.password && this.userForm.password !== this.userForm.confirmPassword) {
                this.userFormErrors.password_confirm = 'Passwords do not match';
                this.renderUsersSection();
                if (this.userModal) this.userModal.show();
                return;
            }

            try {
                const url = this.userEditing
                    ? `/setup/api/users/${this.userForm.username}`
                    : '/setup/api/users';
                const method = this.userEditing ? 'PUT' : 'POST';
                const payload = {
                    name: this.userForm.name,
                    role: this.userForm.role,
                    telegram_id: this.userForm.telegram_id || '',
                    whatsapp_id: this.userForm.whatsapp_id || '',
                    thinking: !!this.userForm.thinking,
                    thinking_level: this.userForm.thinking_level || '',
                    sandbox: !!this.userForm.sandbox
                };
                if (!this.userEditing) {
                    payload.username = this.userForm.username;
                    if (this.userForm.password) payload.password = this.userForm.password;
                } else if (this.userForm.clear_password) {
                    payload.clear_password = true;
                } else if (this.userForm.password) {
                    payload.password = this.userForm.password;
                }

                const resp = await fetch(url, {
                    method,
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(payload)
                });
                const data = await resp.json();
                if (!data.success) {
                    if (data.errors) {
                        this.userFormErrors = data.errors;
                        this.renderUsersSection();
                        if (this.userModal) this.userModal.show();
                        return;
                    }
                    throw new Error(data.message || 'Failed to save user');
                }

                if (this.userModal) this.userModal.hide();
                this.usersSuccess = this.userEditing ? 'User updated' : 'User created';
                await this.loadUsers();
            } catch (err) {
                this.usersError = err.message || 'Failed to save user';
                this.renderUsersSection();
            }
        }

        async deleteUser() {
            this.userDeleting = true;
            try {
                const resp = await fetch(`/setup/api/users/${this.userDeleteUsername}`, { method: 'DELETE' });
                const data = await resp.json();
                if (!data.success) throw new Error(data.message || 'Failed to delete user');
                if (this.userDeleteModal) this.userDeleteModal.hide();
                this.usersSuccess = 'User deleted';
                await this.loadUsers();
            } catch (err) {
                this.usersError = err.message || 'Failed to delete user';
                this.renderUsersSection();
            } finally {
                this.userDeleting = false;
            }
        }
    }

    class SetupWizardController {
        constructor(root) {
            this.$root = $(root);
            this.$progress = $('#wizard-progress-bar');
            this.$stepNumber = $('#wizard-step-number');
            this.$totalSteps = $('#wizard-total-steps');
            this.$errorAlert = $('#wizard-error-alert');
            this.$title = $('#wizard-step-title');
            this.$description = $('#wizard-step-description');
            this.$loading = $('#wizard-loading');
            this.$content = $('#wizard-content');
            this.$stepContent = $('#wizard-step-content');
            this.$review = $('#wizard-review-content');
            this.$prev = $('#wizard-prev-btn');
            this.$next = $('#wizard-next-btn');
            this.$nextLabel = $('#wizard-next-label');
            this.$nextIcon = $('#wizard-next-icon');
            this.$nextSpinner = $('#wizard-next-spinner');
            this.completeModal = new bootstrap.Modal(document.getElementById('completeModal'));

            this.steps = [];
            this.step = 1;
            this.totalSteps = 1;
            this.wizardData = {};
            this.currentStep = {};
            this.fieldErrors = {};
            this.loading = false;
            this.saving = false;
        }

        init() {
            this.bindEvents();
            this.loadState();
        }

        bindEvents() {
            this.$errorAlert.find('.btn-close').on('click', () => hideAlert(this.$errorAlert));
            this.$prev.on('click', () => this.prevStep());
            this.$next.on('click', () => this.nextStep());
            this.$stepContent.on('input change', '.js-bound-field', (event) => this.handleFieldChange(event));
            $('#wizard-close-btn').on('click', () => this.closeWizard());
        }

        async loadState() {
            this.setLoading(true);
            try {
                const resp = await fetch('/setup/api/wizard/state');
                const data = await resp.json();
                if (!data.success) throw new Error(data.message || 'Failed to load wizard state');
                this.steps = data.data.steps || [];
                this.step = data.data.step || 1;
                this.totalSteps = data.data.totalSteps || 1;
                this.wizardData = data.data.data || {};
                await this.loadStep();
            } catch (err) {
                showAlert(this.$errorAlert, err.message || 'Failed to load wizard');
                this.setLoading(false);
            }
        }

        async loadStep() {
            this.setLoading(true);
            this.fieldErrors = {};
            try {
                const resp = await fetch('/setup/api/wizard/step');
                const data = await resp.json();
                if (!data.success) throw new Error(data.message || 'Failed to load step');
                this.currentStep = data.data.stepDef || {};
                this.wizardData = data.data.data || this.wizardData;
                this.$stepContent.html(data.data.formHTML || '');
                this.render();
            } catch (err) {
                showAlert(this.$errorAlert, err.message || 'Failed to load step');
            } finally {
                this.setLoading(false);
            }
        }

        render() {
            const progress = Math.round((this.step / this.totalSteps) * 100);
            this.$progress.css('width', `${progress}%`).attr('aria-valuenow', progress);
            this.$stepNumber.text(this.step);
            this.$totalSteps.text(this.totalSteps);
            this.$title.text(this.currentStep.title || 'Setup');

            if (this.currentStep.description) {
                this.$description.text(this.currentStep.description).removeClass('d-none');
            } else {
                this.$description.addClass('d-none').text('');
            }

            this.populateFields(this.$stepContent, this.wizardData);
            this.applyShowWhen(this.$stepContent, this.wizardData);
            this.renderFieldErrors(this.$stepContent, this.fieldErrors);
            this.renderReview();
            this.syncNav();
        }

        populateFields($container, state) {
            $container.find('.js-bound-field').each((_, el) => {
                const $field = $(el);
                const bindPath = $field.data('bind');
                const bindType = $field.data('bind-type') || 'string';
                const value = getByPath(state, bindPath);
                if ($field.is(':checkbox')) {
                    $field.prop('checked', !!value);
                } else if (bindType === 'string-list') {
                    $field.val(formatStringList(value));
                } else {
                    $field.val(value == null ? '' : value);
                }
            });
        }

        applyShowWhen($container, state) {
            $container.find('[data-showwhen]').each((_, el) => {
                const $el = $(el);
                $el.toggleClass('d-none', !evaluateShowWhen($el.data('showwhen'), state));
            });
        }

        renderFieldErrors($container, errors) {
            $container.find('.js-bound-field').removeClass('is-invalid');
            $container.find('[data-field-error]').each((_, el) => {
                const $error = $(el);
                const fieldPath = $error.data('field-error');
                const message = errors[fieldPath];
                $error.toggleClass('d-none', !message).text(message || '');
                if (message) {
                    $container.find(`.js-bound-field[data-bind="${fieldPath}"]`).addClass('is-invalid');
                }
            });
        }

        handleFieldChange(event) {
            const $field = $(event.currentTarget);
            const bindPath = $field.data('bind');
            const bindType = $field.data('bind-type') || 'string';
            let value;
            if ($field.is(':checkbox')) {
                value = $field.is(':checked');
            } else if (bindType === 'number') {
                const raw = $field.val();
                value = raw === '' ? 0 : Number(raw);
            } else if (bindType === 'string-list') {
                value = parseStringList($field.val());
            } else {
                value = $field.val();
            }
            setByPath(this.wizardData, bindPath, value);
            this.applyShowWhen(this.$stepContent, this.wizardData);

            if (this.currentStep.id === 'llm' && bindPath === 'LLMProviderID' && value && value !== 'custom') {
                this.refreshModelOptions(value);
            }
        }

        async refreshModelOptions(provider) {
            try {
                const resp = await fetch(`/setup/api/wizard/models/${provider}`);
                const data = await resp.json();
                if (data.success && data.data.defaultModel) {
                    this.wizardData.LLMModel = data.data.defaultModel;
                }
                await this.loadStep();
            } catch (_err) {
                // Keep current state if refresh fails.
            }
        }

        renderReview() {
            if (this.currentStep.id !== 'review') {
                this.$review.addClass('d-none').empty();
                return;
            }
            this.$review.removeClass('d-none').html(`
                <h6>Configuration Summary</h6>
                <table class="table table-sm">
                    <tbody>
                        <tr><th>Workspace</th><td>${escapeHtml(this.wizardData.WorkspacePath || '~/.goclaw/workspace')}</td></tr>
                        <tr><th>Owner</th><td>${escapeHtml((this.wizardData.UserDisplayName || '') + ' (' + (this.wizardData.UserName || '') + ')')}</td></tr>
                        <tr><th>HTTP Server</th><td>${escapeHtml(this.wizardData.HTTPEnabled ? this.wizardData.HTTPListen : 'Disabled')}</td></tr>
                        <tr><th>Telegram</th><td>${escapeHtml(this.wizardData.TelegramEnabled ? 'Enabled' : 'Disabled')}</td></tr>
                        <tr><th>WhatsApp</th><td>${escapeHtml(this.wizardData.WhatsAppEnabled ? 'Enabled' : 'Disabled')}</td></tr>
                        <tr><th>LLM Provider</th><td>${escapeHtml(this.wizardData.LLMProviderID || 'Not configured')}</td></tr>
                        <tr><th>Voice LLM</th><td>${escapeHtml(this.wizardData.VoiceLLMEnabled ? `Enabled (${this.wizardData.VoiceLLMVoice || ''})` : 'Disabled')}</td></tr>
                    </tbody>
                </table>
            `);
        }

        syncNav() {
            this.$prev.prop('disabled', this.step <= 1 || this.loading || this.saving);
            this.$next.prop('disabled', this.loading || this.saving);
            this.$nextLabel.text(this.step === this.totalSteps ? 'Finish' : 'Next');
            this.$nextIcon.attr('class', `bi ${this.step === this.totalSteps ? 'bi-check-lg' : 'bi-arrow-right'}`);
            this.$nextSpinner.toggleClass('d-none', !this.saving);
        }

        setLoading(isLoading) {
            this.loading = isLoading;
            this.$loading.toggleClass('d-none', !isLoading);
            this.$content.toggleClass('d-none', isLoading);
            this.syncNav();
        }

        async prevStep() {
            if (this.step <= 1) return;
            this.setLoading(true);
            showAlert(this.$errorAlert, '');
            try {
                const resp = await fetch('/setup/api/wizard/prev', { method: 'POST' });
                const data = await resp.json();
                if (!data.success) throw new Error(data.message || 'Failed to go back');
                this.step = data.data.step;
                await this.loadStep();
            } catch (err) {
                showAlert(this.$errorAlert, err.message || 'Failed to go back');
                this.setLoading(false);
            }
        }

        async nextStep() {
            showAlert(this.$errorAlert, '');
            this.fieldErrors = {};

            if (this.step === this.totalSteps) {
                await this.finish();
                return;
            }

            this.setLoading(true);
            try {
                const resp = await fetch('/setup/api/wizard/submit', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(this.wizardData)
                });
                const data = await resp.json();
                if (!data.success) {
                    if (data.errors) {
                        this.fieldErrors = data.errors;
                        this.renderFieldErrors(this.$stepContent, this.fieldErrors);
                        showAlert(this.$errorAlert, 'Please fix the errors below');
                        this.setLoading(false);
                        return;
                    }
                    throw new Error(data.message || 'Validation failed');
                }
                this.step = data.data.step;
                await this.loadStep();
            } catch (err) {
                showAlert(this.$errorAlert, err.message || 'Validation failed');
                this.setLoading(false);
            }
        }

        async finish() {
            this.saving = true;
            this.syncNav();
            showAlert(this.$errorAlert, '');
            try {
                let resp = await fetch('/setup/api/wizard/submit', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(this.wizardData)
                });
                let data = await resp.json();
                if (!data.success && data.errors) {
                    this.fieldErrors = data.errors;
                    this.renderFieldErrors(this.$stepContent, this.fieldErrors);
                    showAlert(this.$errorAlert, 'Please fix the errors before finishing');
                    return;
                }

                resp = await fetch('/setup/api/wizard/finish', { method: 'POST' });
                data = await resp.json();
                if (!data.success) throw new Error(data.message || 'Failed to save configuration');
                this.completeModal.show();
            } catch (err) {
                showAlert(this.$errorAlert, err.message || 'Failed to save configuration');
            } finally {
                this.saving = false;
                this.syncNav();
            }
        }

        async closeWizard() {
            if (typeof window.closeWebview === 'function') {
                try {
                    await fetch('/setup/api/shutdown', { method: 'POST' });
                } catch (_err) {
                    // Expected during shutdown.
                }
                window.closeWebview();
                return;
            }

            $('#wizard-close-btn').addClass('d-none');
            $('#wizard-close-message').removeClass('d-none');
            try {
                await fetch('/setup/api/shutdown', { method: 'POST' });
            } catch (_err) {
                // Expected during shutdown.
            }
        }
    }

    $(function () {
        const editorRoot = document.getElementById('setup-editor-root');
        if (editorRoot) {
            const controller = new SetupEditorController(editorRoot);
            controller.init();
        }

        const wizardRoot = document.getElementById('setup-wizard-root');
        if (wizardRoot) {
            const controller = new SetupWizardController(wizardRoot);
            controller.init();
        }
    });
}(window, window.jQuery));
