// Bootstrap/jQuery runtime for the GoClaw setup editor and wizard.
(function (window, $) {
    'use strict';

    const PROVIDER_DRIVER_OPTIONS = [];

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

    function parseLineList(value) {
        return String(value || '')
            .split('\n')
            .map(s => s.trim())
            .filter(Boolean);
    }

    function formatDateTime(value) {
        if (!value) return '-';
        const date = new Date(value);
        if (Number.isNaN(date.getTime())) return '-';
        return date.toLocaleString();
    }

    function formatBytes(value) {
        const bytes = Number(value || 0);
        if (!Number.isFinite(bytes) || bytes <= 0) return '-';
        const units = ['B', 'KB', 'MB', 'GB', 'TB'];
        let size = bytes;
        let unitIndex = 0;
        while (size >= 1024 && unitIndex < units.length - 1) {
            size /= 1024;
            unitIndex += 1;
        }
        const precision = size >= 10 || unitIndex === 0 ? 0 : 1;
        return `${size.toFixed(precision)} ${units[unitIndex]}`;
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

    function extractApplyResult(data) {
        if (!data || !data.data || !data.data.apply) return null;
        return data.data.apply;
    }

    async function fetchGatewayStatus() {
        const resp = await fetch('/api/status', { cache: 'no-store' });
        if (!resp.ok) {
            throw new Error(`status ${resp.status}`);
        }
        return resp.json();
    }

    async function captureCurrentInstanceID() {
        try {
            const status = await fetchGatewayStatus();
            return status && status.instanceID ? status.instanceID : null;
        } catch (_err) {
            return null;
        }
    }

    async function waitForGatewayRestart(previousInstanceID, onUpdate) {
        const start = Date.now();
        const timeoutMs = 60000;
        let sawOldInstance = false;
        let sawOffline = false;

        while (Date.now() - start < timeoutMs) {
            try {
                const status = await fetchGatewayStatus();
                const instanceID = status && status.instanceID;
                const sameInstance = previousInstanceID && instanceID === previousInstanceID;

                if (!previousInstanceID) {
                    return { ready: true, status };
                }

                if (sameInstance) {
                    sawOldInstance = true;
                    if (typeof onUpdate === 'function') {
                        onUpdate({
                            phase: 'waiting_for_stop',
                            elapsedMs: Date.now() - start,
                            status,
                        });
                    }
                } else if (sawOldInstance || sawOffline) {
                    return { ready: true, status };
                } else {
                    // We might have attached after the old process already died.
                    return { ready: true, status };
                }
            } catch (_err) {
                sawOffline = true;
                if (typeof onUpdate === 'function') {
                    onUpdate({
                        phase: 'waiting_for_start',
                        elapsedMs: Date.now() - start,
                    });
                }
            }

            await new Promise(resolve => setTimeout(resolve, 1000));
        }

        return { ready: false };
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
            this.$topApply = $('#editor-top-apply');
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
            this.applyPending = false;
            this.lastKnownInstanceID = null;

            this.mcMeta = {};
            this.mcLoading = {};
            this.mcExpanded = {};
            this.mcProviders = [];
            this.mcProvidersByPurpose = {};
            this.mcProvidersLoadedByPurpose = {};
            this.mcModalField = '';
            this.mcModalPurpose = '';
            this.mcModalStep = 1;
            this.mcSelectedProvider = null;
            this.mcSelectedModel = null;
            this.mcManualModelID = '';
            this.mcAvailableModels = [];
            this.mcModelSearch = '';
            this.mcDrag = null;
            this.mcModal = new bootstrap.Modal(document.getElementById('mcModal'));

            this.providerUi = {};
            this.providerPresets = [];
            this.providerPresetsLoaded = false;
            this.providerDriverOptions = [...PROVIDER_DRIVER_OPTIONS];
            this.providerDriverOptionsLoaded = false;

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
            this.a2aPeers = [];
            this.a2aPeerUsers = [];
            this.a2aRuntimePeers = [];
            this.a2aRuntimeStatus = {};
            this.a2aPairing = {};
            this.a2aPeersLoading = false;
            this.a2aPeersError = '';
            this.a2aPeersSuccess = '';
            this.a2aPeerForm = {};
            this.a2aPeerFormErrors = {};
            this.a2aPeerEditing = false;
            this.a2aPeerDeleting = false;
            this.a2aPeerDeleteID = '';
            this.a2aPeerModal = null;
            this.a2aPeerDeleteModal = null;
            this.a2aPingTarget = '';
            this.a2aPingResult = null;
            this.pendingOwnerPairings = {};
            this.editorPairingStatus = {};
            this.editorPairingPollers = {};
            this.pendingSubsectionTarget = '';
            this.editorPairingSessions = {
                telegram: `web-editor-telegram-${Date.now()}`,
                whatsapp: `web-editor-whatsapp-${Date.now()}`
            };
            this.localLLMState = null;
            this.localLLMError = '';
            this.localLLMActionPending = false;
            this.localLLMPollTimer = null;
            this.restartModal = new bootstrap.Modal(document.getElementById('editorRestartModal'));
            this.$restartMessage = $('#editor-restart-message');
            this.$restartDetail = $('#editor-restart-detail');
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
            this.$root.on('click', '.sidebar-item[data-dashboard-home="true"]', (event) => {
                event.preventDefault();
                this.showDashboard();
            });

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
                const $task = $(event.currentTarget);
                const sectionId = $task.data('section-target');
                const subsectionTarget = $task.data('subsection-target') || '';
                if (sectionId) this.switchSection(sectionId, subsectionTarget);
            });

            this.$topSave.on('click', () => this.saveAll());
            this.$topApply.on('click', () => this.applySaved());
            this.$topDiscard.on('click', () => this.discardAll());

            this.$errorAlert.find('.btn-close').on('click', () => hideAlert(this.$errorAlert));
            this.$successAlert.find('.btn-close').on('click', () => hideAlert(this.$successAlert));
            this.$formContent.on('input change', '.js-bound-field', (event) => this.handleBoundFieldChange(event));
            this.$formContent.on('change', '.js-select-custom-select', (event) => this.handleSelectCustomChange(event));
            this.$formContent.on('input change', '.js-select-custom-input', (event) => this.handleSelectCustomInput(event));
            this.$formContent.on('click', '.js-model-chain-add', (event) => this.openModelModal(
                $(event.currentTarget).data('field-path'),
                $(event.currentTarget).data('purpose') || ''
            ));
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
            this.$formContent.on('click', '.js-provider-open-local-llm', () => this.switchSection('local-llm'));
            this.$formContent.on('input change', '.js-provider-input', (event) => this.handleProviderInput(event));
            this.$formContent.on('click', '.js-provider-save-new', (event) => this.saveNewProvider($(event.currentTarget).data('field-path')));

            this.$formContent.on('click', '.js-role-toggle', (event) => this.toggleRole($(event.currentTarget).data('field-path'), $(event.currentTarget).data('role-name')));
            this.$formContent.on('click', '.js-role-delete', (event) => this.deleteRole($(event.currentTarget).data('field-path'), $(event.currentTarget).data('role-name')));
            this.$formContent.on('click', '.js-role-add-start', (event) => this.startAddRole($(event.currentTarget).data('field-path')));
            this.$formContent.on('click', '.js-role-add-cancel', (event) => this.cancelAddRole($(event.currentTarget).data('field-path')));
            this.$formContent.on('input change', '.js-role-input', (event) => this.handleRoleInput(event));
            this.$formContent.on('click', '.js-role-save-new', (event) => this.saveNewRole($(event.currentTarget).data('field-path')));
            this.$formContent.on('click', '.js-form-action', (event) => this.runFormAction(event));
            this.$formContent.on('click', '.js-editor-pairing-start', () => this.startEditorPairing());
            this.$formContent.on('click', '.js-editor-pairing-refresh', () => this.refreshEditorPairing());

            $('#mcModalBack').on('click', () => this.showModelProviderStep());
            $('#mcModalAdd').on('click', () => this.addSelectedModelToChain());
            $('#mcModelSearch').on('input', (event) => {
                this.mcModelSearch = $(event.currentTarget).val() || '';
                this.renderModelModal();
            });
            $('#mcManualModelID').on('input', (event) => {
                this.mcManualModelID = ($(event.currentTarget).val() || '').trim();
                if (this.mcManualModelID) {
                    this.mcSelectedModel = null;
                }
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
            return Object.values(this.dirtyState).some(Boolean) || Object.keys(this.pendingOwnerPairings).length > 0;
        }

        dirtyCount() {
            return Object.values(this.dirtyState).filter(Boolean).length + (Object.keys(this.pendingOwnerPairings).length > 0 ? 1 : 0);
        }

        currentDirty() {
            if (!this.currentSection || this.currentSectionType === 'custom') return false;
            return JSON.stringify(this.formData) !== JSON.stringify(this.originalData);
        }

        syncTopBar() {
            const hasDirty = this.hasAnyDirty();
            const show = hasDirty || this.applyPending;
            this.$topActions.toggleClass('d-none', !show);
            this.$topSave.prop('disabled', !hasDirty || this.saving);
            this.$topApply.prop('disabled', this.saving || hasDirty || !this.applyPending);
            this.$topDiscard.prop('disabled', !hasDirty || this.saving);

            if (!show) {
                this.$topDirtyLabel.text('');
            } else if (hasDirty && this.dirtyCount() === 1 && this.currentSection && this.dirtyState[this.currentSection]) {
                this.$topDirtyLabel.text(this.currentTitle ? `Unsaved: ${this.currentTitle}` : 'Unsaved changes');
            } else if (hasDirty) {
                this.$topDirtyLabel.text(`Unsaved: ${this.dirtyCount()} sections`);
            } else if (this.applyPending) {
                this.$topDirtyLabel.text('Saved. Apply pending');
            } else {
                this.$topDirtyLabel.text('');
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
            } else {
                $('.sidebar-item[data-dashboard-home="true"]').addClass('active');
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

        async switchSection(sectionId, subsectionTarget = '') {
            const target = String(subsectionTarget || '');
            if (this.currentSection === sectionId) {
                this.pendingSubsectionTarget = target;
                if (target) {
                    this.activatePendingSubsection();
                }
                return;
            }
            this.pendingSubsectionTarget = target;
            this.cacheCurrentSectionState();
            Object.values(this.editorPairingPollers).forEach((timer) => window.clearTimeout(timer));
            this.editorPairingPollers = {};
            await this.loadSection(sectionId);
        }

        showDashboard() {
            this.cacheCurrentSectionState();
            this.stopLocalLLMPolling();
            this.currentSection = '';
            this.currentTitle = 'Dashboard';
            this.currentSectionType = '';
            this.fieldErrors = {};
            this.$loading.addClass('d-none');
            this.$sectionPanel.addClass('d-none');
            this.$dashboard.removeClass('d-none');
            showAlert(this.$errorAlert, '');
            showAlert(this.$successAlert, '');
            this.markSidebarActive();
            this.syncTopBar();
        }

        async loadSection(sectionId) {
            this.loading = true;
            this.fieldErrors = {};
            if (sectionId !== 'local-llm') {
                this.stopLocalLLMPolling();
            }
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
                    } else if (sectionId === 'a2a-peers') {
                        await this.loadA2APeers();
                    } else if (sectionId === 'local-llm') {
                        await this.loadLocalLLM();
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
            this.renderSelectCustomWidgets();
            this.initModelWidgets();
            this.renderProviderLists();
            this.renderRolesLists();
            this.renderEditorPairingPanel();
            this.activatePendingSubsection();
        }

        activatePendingSubsection() {
            const target = String(this.pendingSubsectionTarget || '').trim();
            if (!target || !this.$formContent.length) {
                return;
            }
            const panel = document.getElementById(target);
            if (!panel || !this.$formContent.has(panel).length) {
                return;
            }
            const collapse = bootstrap.Collapse.getOrCreateInstance(panel, { toggle: false });
            collapse.show();
            const sectionCard = panel.closest('.js-form-section');
            window.requestAnimationFrame(() => {
                if (sectionCard && typeof sectionCard.scrollIntoView === 'function') {
                    sectionCard.scrollIntoView({ behavior: 'smooth', block: 'start' });
                }
            });
            this.pendingSubsectionTarget = '';
        }

        renderEditorPairingPanel() {
            this.$formContent.find('.js-editor-pairing-panel').remove();
            if (!['telegram', 'whatsapp'].includes(this.currentSection)) {
                return;
            }

            const channel = this.currentSection;
            const staged = this.pendingOwnerPairings[channel] || '';
            this.$formContent.append(`
                <div class="card mt-4 js-editor-pairing-panel">
                    <div class="card-header d-flex justify-content-between align-items-center">
                        <span>Owner Pairing</span>
                        <span class="badge text-bg-secondary js-editor-pairing-badge">Not started</span>
                    </div>
                    <div class="card-body">
                        <p class="text-muted js-editor-pairing-message mb-2">Pairing status will appear here.</p>
                        <div class="alert alert-light border d-none js-editor-pairing-artifact"></div>
                        <div class="small text-muted mb-3 js-editor-pairing-identity">${staged ? `Staged owner ID: ${escapeHtml(staged)}` : ''}</div>
                        <div class="d-flex gap-2 flex-wrap">
                            <button type="button" class="btn btn-sm btn-primary js-editor-pairing-start">Start Pairing</button>
                            <button type="button" class="btn btn-sm btn-outline-secondary js-editor-pairing-refresh">Refresh</button>
                        </div>
                        <div class="form-text mt-2">Successful pairing is staged locally and written to <code>users.json</code> on Save Changes.</div>
                    </div>
                </div>
            `);
            this.refreshEditorPairing();
        }

        async startEditorPairing() {
            if (!['telegram', 'whatsapp'].includes(this.currentSection)) return;
            const channel = this.currentSection;
            try {
                const payload = {
                    sessionId: this.editorPairingSessions[channel],
                    surface: 'web-editor'
                };
                if (channel === 'telegram') {
                    payload.botToken = this.formData.botToken || '';
                    if (!payload.botToken) throw new Error('Telegram bot token is required before pairing');
                }
                this.pendingOwnerPairings[channel] = '';
                const resp = await fetch(`/setup/api/pairing/${encodeURIComponent(channel)}/start`, {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(payload)
                });
                const data = await resp.json();
                if (!data.success) throw new Error(data.message || `Failed to start ${channel} pairing`);
                this.applyEditorPairingStatus(channel, data.data || {});
            } catch (err) {
                showAlert(this.$errorAlert, err.message || `Failed to start ${channel} pairing`);
            }
        }

        async refreshEditorPairing() {
            if (!['telegram', 'whatsapp'].includes(this.currentSection)) return;
            const channel = this.currentSection;
            try {
                const resp = await fetch(`/setup/api/pairing/${encodeURIComponent(channel)}/status`, {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        sessionId: this.editorPairingSessions[channel],
                        surface: 'web-editor'
                    })
                });
                const data = await resp.json();
                if (!data.success) throw new Error(data.message || `Failed to load ${channel} pairing status`);
                this.applyEditorPairingStatus(channel, data.data || {});
            } catch (err) {
                showAlert(this.$errorAlert, err.message || `Failed to load ${channel} pairing status`);
            }
        }

        applyEditorPairingStatus(channel, status) {
            this.editorPairingStatus[channel] = status || {};
            if (status && status.identity && status.identity.id) {
                this.pendingOwnerPairings[channel] = status.identity.id;
            }
            const $panel = this.$formContent.find('.js-editor-pairing-panel');
            if (!$panel.length) return;
            const badge = $panel.find('.js-editor-pairing-badge');
            const message = $panel.find('.js-editor-pairing-message');
            const artifact = $panel.find('.js-editor-pairing-artifact');
            const identity = $panel.find('.js-editor-pairing-identity');
            const state = String(status.state || 'not_started');
            const badgeMap = {
                not_started: ['text-bg-secondary', 'Not started'],
                waiting: ['text-bg-warning', 'Waiting'],
                paired: ['text-bg-success', 'Paired'],
                expired: ['text-bg-danger', 'Expired'],
                failed: ['text-bg-danger', 'Failed'],
                cancelled: ['text-bg-secondary', 'Cancelled']
            };
            const badgeInfo = badgeMap[state] || badgeMap.not_started;
            badge.attr('class', `badge ${badgeInfo[0]} js-editor-pairing-badge`).text(badgeInfo[1]);
            message.text(status.message || `${channel} pairing has not started yet.`);

            if (channel === 'telegram' && status.artifacts && status.artifacts.code) {
                artifact.removeClass('d-none').html(`<div class="fw-semibold mb-1">One-time code</div><code class="fs-5">${escapeHtml(status.artifacts.code)}</code>`);
            } else if (channel === 'whatsapp' && status.artifacts && status.artifacts.qrCode) {
                artifact.removeClass('d-none').html(`
                    <div class="fw-semibold mb-2">Scan this QR code</div>
                    <div class="bg-white rounded p-3 d-inline-block js-editor-whatsapp-qr"></div>
                    <div class="small text-muted mt-2">${escapeHtml(status.artifacts.qrLabel || '')}</div>
                `);
                const container = artifact.find('.js-editor-whatsapp-qr').get(0);
                if (container && typeof window.QRCode !== 'undefined') {
                    container.innerHTML = '';
                    // eslint-disable-next-line no-new
                    new window.QRCode(container, { text: status.artifacts.qrCode, width: 220, height: 220 });
                }
            } else {
                artifact.addClass('d-none').empty();
            }

            const staged = this.pendingOwnerPairings[channel] || '';
            if (status.identity) {
                identity.text(`Staged owner ID: ${status.identity.id}`);
            } else {
                identity.text(staged ? `Staged owner ID: ${staged}` : '');
            }
            if (this.editorPairingPollers[channel]) {
                window.clearTimeout(this.editorPairingPollers[channel]);
                delete this.editorPairingPollers[channel];
            }
            if (state === 'waiting') {
                const delay = Number(status.pollAfterMs) > 0 ? Number(status.pollAfterMs) : 1500;
                this.editorPairingPollers[channel] = window.setTimeout(() => {
                    if (this.currentSection === channel) {
                        this.refreshEditorPairing();
                    }
                }, delay);
            }
            this.syncTopBar();
        }

        populateBoundFields($container, state) {
            $container.find('.js-bound-field').each((_, el) => {
                const $field = $(el);
                const bindPath = $field.data('bind');
                const bindType = $field.data('bind-type') || 'string';
                const scale = this.readNumericScale($field);
                const value = getByPath(state, bindPath);
                if ($field.is(':checkbox')) {
                    $field.prop('checked', !!value);
                } else if (bindType === 'string-list') {
                    $field.val(formatStringList(value));
                } else if (bindType === 'number') {
                    $field.val(value == null ? '' : value / scale);
                    this.updateSliderDisplay($field);
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
            $container.find('.js-select-custom-select, .js-select-custom-input').removeClass('is-invalid');
            $container.find('[data-field-error]').each((_, el) => {
                const $error = $(el);
                const fieldPath = $error.data('field-error');
                const message = errors[fieldPath];
                $error.toggleClass('d-none', !message).text(message || '');
                if (message) {
                    $container.find(`.js-bound-field[data-bind="${fieldPath}"]`).addClass('is-invalid');
                    $container.find(`.js-select-custom[data-field-path="${fieldPath}"] .js-select-custom-select`).addClass('is-invalid');
                    $container.find(`.js-select-custom[data-field-path="${fieldPath}"] .js-select-custom-input`).addClass('is-invalid');
                }
            });
        }

        handleBoundFieldChange(event) {
            const $field = $(event.currentTarget);
            const bindPath = $field.data('bind');
            const bindType = $field.data('bind-type') || 'string';
            const scale = this.readNumericScale($field);

            let value;
            if ($field.is(':checkbox')) {
                value = $field.is(':checked');
            } else if (bindType === 'number') {
                const raw = $field.val();
                value = raw === '' ? 0 : Math.round(Number(raw) * scale);
                this.updateSliderDisplay($field);
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

        readNumericScale($field) {
            const raw = Number($field.data('scale'));
            return Number.isFinite(raw) && raw > 0 ? raw : 1;
        }

        initializePairingStep() {
            if (!this.currentStep || this.currentStep.id !== 'pairing') {
                return;
            }
            this.refreshAllPairings();
        }

        stopAllPairingPollers() {
            Object.values(this.pairingPollers).forEach((timer) => {
                if (timer) window.clearTimeout(timer);
            });
            this.pairingPollers = {};
        }

        async refreshAllPairings() {
            const channels = [];
            if (this.wizardData.TelegramEnabled) channels.push('telegram');
            if (this.wizardData.WhatsAppEnabled) channels.push('whatsapp');
            await Promise.all(channels.map((channel) => this.fetchPairingStatus(channel)));
            this.syncNav();
        }

        async refreshPairing(event) {
            const channel = $(event.currentTarget).closest('[data-pairing-channel]').data('pairing-channel');
            if (!channel) return;
            await this.fetchPairingStatus(channel);
            this.syncNav();
        }

        async startPairing(event) {
            const channel = $(event.currentTarget).closest('[data-pairing-channel]').data('pairing-channel');
            if (!channel) return;
            try {
                const resp = await fetch(`/setup/api/wizard/pairing/${encodeURIComponent(channel)}/start`, { method: 'POST' });
                const data = await resp.json();
                if (!data.success) throw new Error(data.message || `Failed to start ${channel} pairing`);
                this.applyPairingStatus(channel, data.data || {});
                this.schedulePairingPoll(channel, data.data || {});
                this.syncNav();
            } catch (err) {
                showAlert(this.$errorAlert, err.message || `Failed to start ${channel} pairing`);
            }
        }

        async cancelPairing(event) {
            const channel = $(event.currentTarget).closest('[data-pairing-channel]').data('pairing-channel');
            if (!channel) return;
            try {
                const resp = await fetch(`/setup/api/wizard/pairing/${encodeURIComponent(channel)}/cancel`, { method: 'POST' });
                const data = await resp.json();
                if (!data.success) throw new Error(data.message || `Failed to cancel ${channel} pairing`);
                this.applyPairingStatus(channel, data.data || {});
                this.schedulePairingPoll(channel, data.data || {});
                this.syncNav();
            } catch (err) {
                showAlert(this.$errorAlert, err.message || `Failed to cancel ${channel} pairing`);
            }
        }

        async fetchPairingStatus(channel) {
            try {
                const resp = await fetch(`/setup/api/wizard/pairing/${encodeURIComponent(channel)}/status`);
                const data = await resp.json();
                if (!data.success) throw new Error(data.message || `Failed to load ${channel} pairing status`);
                this.applyPairingStatus(channel, data.data || {});
                this.schedulePairingPoll(channel, data.data || {});
            } catch (err) {
                this.renderPairingError(channel, err.message || `Failed to load ${channel} pairing status`);
            }
        }

        schedulePairingPoll(channel, status) {
            if (this.pairingPollers[channel]) {
                window.clearTimeout(this.pairingPollers[channel]);
                delete this.pairingPollers[channel];
            }
            const state = status && status.state ? status.state : '';
            if (['paired', 'expired', 'failed', 'cancelled', 'not_started'].includes(state)) {
                return;
            }
            const delay = Number(status && status.pollAfterMs) > 0 ? Number(status.pollAfterMs) : 1500;
            this.pairingPollers[channel] = window.setTimeout(() => this.fetchPairingStatus(channel), delay);
        }

        applyPairingStatus(channel, status) {
            this.pairingStatus[channel] = status || {};
            if (channel === 'telegram' && status && status.identity && status.identity.id) {
                this.wizardData.UserTelegramID = status.identity.id;
            }
            if (channel === 'whatsapp' && status && status.identity && status.identity.id) {
                this.wizardData.UserWhatsAppID = status.identity.id;
            }
            this.renderPairingStatus(channel, status || {});
        }

        renderPairingError(channel, message) {
            const $card = this.$stepContent.find(`[data-pairing-channel="${channel}"]`);
            if (!$card.length) return;
            $card.removeClass('border-success bg-success-subtle');
            $card.find('.js-pairing-badge').attr('class', 'badge text-bg-danger js-pairing-badge').text('Error');
            $card.find('.js-pairing-message').text(message || 'Pairing failed.');
            $card.find('.js-pairing-success').addClass('d-none');
        }

        renderPairingStatus(channel, status) {
            const $card = this.$stepContent.find(`[data-pairing-channel="${channel}"]`);
            if (!$card.length) return;

            const state = String(status.state || 'not_started');
            const badge = $card.find('.js-pairing-badge');
            const message = $card.find('.js-pairing-message');
            const success = $card.find('.js-pairing-success');
            const successText = $card.find('.js-pairing-success-text');
            const artifact = $card.find('.js-pairing-artifact');
            const identity = $card.find('.js-pairing-identity');
            const startBtn = $card.find('.js-pairing-start');
            const cancelBtn = $card.find('.js-pairing-cancel');

            const badgeMap = {
                not_started: ['text-bg-secondary', 'Not started'],
                waiting: ['text-bg-warning', 'Waiting'],
                paired: ['text-bg-success', 'Paired'],
                expired: ['text-bg-danger', 'Expired'],
                failed: ['text-bg-danger', 'Failed'],
                cancelled: ['text-bg-secondary', 'Cancelled']
            };
            const badgeInfo = badgeMap[state] || badgeMap.not_started;
            badge.attr('class', `badge ${badgeInfo[0]} js-pairing-badge`).text(badgeInfo[1]);
            message.text(status.message || `${channel} pairing has not started yet.`);
            $card.toggleClass('border-success', state === 'paired');
            $card.toggleClass('bg-success-subtle', state === 'paired');

            startBtn.text(state === 'paired' ? 'Restart Pairing' : 'Start Pairing');
            cancelBtn.toggleClass('d-none', state !== 'waiting');

            if (state === 'paired') {
                const successParts = [];
                if (channel === 'telegram' && status.identity) {
                    successParts.push('Telegram owner confirmed');
                    if (status.identity.displayName) successParts.push(status.identity.displayName);
                    if (status.identity.id) successParts.push(status.identity.id);
                } else if (channel === 'whatsapp' && status.identity) {
                    successParts.push('WhatsApp owner confirmed');
                    if (status.identity.phone) successParts.push(status.identity.phone);
                    if (status.identity.jid) successParts.push(status.identity.jid);
                } else {
                    successParts.push('Pairing complete');
                }
                success.removeClass('d-none');
                successText.text(`${successParts.join(' · ')}. You can continue to the next step.`);
            } else {
                success.addClass('d-none');
            }

            this.renderPairingArtifact(channel, artifact, status);
            this.renderPairingIdentity(identity, channel, status);
        }

        renderPairingArtifact(channel, $artifact, status) {
            const artifacts = status && status.artifacts ? status.artifacts : {};
            const state = String(status && status.state ? status.state : '');
            if (state === 'paired') {
                $artifact.addClass('d-none').empty();
                return;
            }
            if (channel === 'telegram' && artifacts.code) {
                $artifact.removeClass('d-none').html(`
                    <div class="fw-semibold mb-1">One-time code</div>
                    <code class="fs-5">${escapeHtml(artifacts.code)}</code>
                    <div class="small text-muted mt-1">Send this exact code to the Telegram bot from the owner account.</div>
                `);
                return;
            }
            if (channel === 'whatsapp' && artifacts.qrCode) {
                $artifact.removeClass('d-none').html(`
                    <div class="fw-semibold mb-2">Scan this QR code</div>
                    <div class="bg-white rounded p-3 d-inline-block js-whatsapp-qr"></div>
                    <div class="small text-muted mt-2">${escapeHtml(artifacts.qrLabel || '')}</div>
                `);
                const container = $artifact.find('.js-whatsapp-qr').get(0);
                if (container && typeof window.QRCode !== 'undefined') {
                    container.innerHTML = '';
                    // eslint-disable-next-line no-new
                    new window.QRCode(container, {
                        text: artifacts.qrCode,
                        width: 220,
                        height: 220
                    });
                }
                return;
            }
            $artifact.addClass('d-none').empty();
        }

        renderPairingIdentity($identity, channel, status) {
            const identity = status && status.identity ? status.identity : null;
            const staged = channel === 'telegram' ? this.wizardData.UserTelegramID : this.wizardData.UserWhatsAppID;
            if (identity) {
                if (channel === 'telegram') {
                    const parts = [identity.displayName, identity.username ? `@${identity.username}` : '', identity.id].filter(Boolean);
                    $identity.html(`<span class="text-success"><i class="bi bi-check2-square me-1"></i>Paired owner: ${escapeHtml(parts.join(' · '))}</span>`);
                } else {
                    const parts = [identity.phone, identity.jid || identity.id].filter(Boolean);
                    $identity.html(`<span class="text-success"><i class="bi bi-check2-square me-1"></i>Paired owner: ${escapeHtml(parts.join(' · '))}</span>`);
                }
                return;
            }
            if (staged) {
                $identity.text(`Currently staged owner ID: ${staged}`);
                return;
            }
            $identity.text('');
        }

        initializePairingStep() {
            if (!this.currentStep || this.currentStep.id !== 'pairing') {
                return;
            }
            this.refreshAllPairings();
        }

        stopAllPairingPollers() {
            Object.values(this.pairingPollers).forEach((timer) => {
                if (timer) window.clearTimeout(timer);
            });
            this.pairingPollers = {};
        }

        async refreshAllPairings() {
            const channels = [];
            if (this.wizardData.TelegramEnabled) channels.push('telegram');
            if (this.wizardData.WhatsAppEnabled) channels.push('whatsapp');
            await Promise.all(channels.map((channel) => this.fetchPairingStatus(channel)));
            this.syncNav();
        }

        async refreshPairing(event) {
            const channel = $(event.currentTarget).closest('[data-pairing-channel]').data('pairing-channel');
            if (!channel) return;
            await this.fetchPairingStatus(channel);
            this.syncNav();
        }

        async startPairing(event) {
            const channel = $(event.currentTarget).closest('[data-pairing-channel]').data('pairing-channel');
            if (!channel) return;
            try {
                const resp = await fetch(`/setup/api/wizard/pairing/${encodeURIComponent(channel)}/start`, { method: 'POST' });
                const data = await resp.json();
                if (!data.success) throw new Error(data.message || `Failed to start ${channel} pairing`);
                this.applyPairingStatus(channel, data.data || {});
                this.schedulePairingPoll(channel, data.data || {});
                this.syncNav();
            } catch (err) {
                showAlert(this.$errorAlert, err.message || `Failed to start ${channel} pairing`);
            }
        }

        async cancelPairing(event) {
            const channel = $(event.currentTarget).closest('[data-pairing-channel]').data('pairing-channel');
            if (!channel) return;
            try {
                const resp = await fetch(`/setup/api/wizard/pairing/${encodeURIComponent(channel)}/cancel`, { method: 'POST' });
                const data = await resp.json();
                if (!data.success) throw new Error(data.message || `Failed to cancel ${channel} pairing`);
                this.applyPairingStatus(channel, data.data || {});
                this.schedulePairingPoll(channel, data.data || {});
                this.syncNav();
            } catch (err) {
                showAlert(this.$errorAlert, err.message || `Failed to cancel ${channel} pairing`);
            }
        }

        async fetchPairingStatus(channel) {
            try {
                const resp = await fetch(`/setup/api/wizard/pairing/${encodeURIComponent(channel)}/status`);
                const data = await resp.json();
                if (!data.success) throw new Error(data.message || `Failed to load ${channel} pairing status`);
                this.applyPairingStatus(channel, data.data || {});
                this.schedulePairingPoll(channel, data.data || {});
            } catch (err) {
                this.renderPairingError(channel, err.message || `Failed to load ${channel} pairing status`);
            }
        }

        schedulePairingPoll(channel, status) {
            if (this.pairingPollers[channel]) {
                window.clearTimeout(this.pairingPollers[channel]);
                delete this.pairingPollers[channel];
            }
            const state = status && status.state ? status.state : '';
            if (['paired', 'expired', 'failed', 'cancelled', 'not_started'].includes(state)) {
                return;
            }
            const delay = Number(status && status.pollAfterMs) > 0 ? Number(status.pollAfterMs) : 1500;
            this.pairingPollers[channel] = window.setTimeout(() => this.fetchPairingStatus(channel), delay);
        }

        applyPairingStatus(channel, status) {
            this.pairingStatus[channel] = status || {};
            if (channel === 'telegram' && status && status.identity && status.identity.id) {
                this.wizardData.UserTelegramID = status.identity.id;
            }
            if (channel === 'whatsapp' && status && status.identity && status.identity.id) {
                this.wizardData.UserWhatsAppID = status.identity.id;
            }
            this.renderPairingStatus(channel, status || {});
        }

        renderPairingError(channel, message) {
            const $card = this.$stepContent.find(`[data-pairing-channel="${channel}"]`);
            if (!$card.length) return;
            $card.removeClass('border-success bg-success-subtle');
            $card.find('.js-pairing-badge').attr('class', 'badge text-bg-danger js-pairing-badge').text('Error');
            $card.find('.js-pairing-message').text(message || 'Pairing failed.');
            $card.find('.js-pairing-success').addClass('d-none');
        }

        renderPairingStatus(channel, status) {
            const $card = this.$stepContent.find(`[data-pairing-channel="${channel}"]`);
            if (!$card.length) return;

            const state = String(status.state || 'not_started');
            const staged = channel === 'telegram' ? this.wizardData.UserTelegramID : this.wizardData.UserWhatsAppID;
            const effectivelyPaired = state === 'paired' || (state === 'not_started' && !!staged);
            const badge = $card.find('.js-pairing-badge');
            const message = $card.find('.js-pairing-message');
            const success = $card.find('.js-pairing-success');
            const successText = $card.find('.js-pairing-success-text');
            const artifact = $card.find('.js-pairing-artifact');
            const identity = $card.find('.js-pairing-identity');
            const startBtn = $card.find('.js-pairing-start');
            const cancelBtn = $card.find('.js-pairing-cancel');

            const badgeMap = {
                not_started: ['text-bg-secondary', 'Not started'],
                waiting: ['text-bg-warning', 'Waiting'],
                paired: ['text-bg-success', 'Paired'],
                already_paired: ['text-bg-success', 'Already paired'],
                expired: ['text-bg-danger', 'Expired'],
                failed: ['text-bg-danger', 'Failed'],
                cancelled: ['text-bg-secondary', 'Cancelled']
            };
            const visualState = effectivelyPaired && state !== 'paired' ? 'already_paired' : state;
            const badgeInfo = badgeMap[visualState] || badgeMap.not_started;
            badge.attr('class', `badge ${badgeInfo[0]} js-pairing-badge`).text(badgeInfo[1]);
            if (effectivelyPaired && state !== 'paired') {
                message.text(`${channel.charAt(0).toUpperCase() + channel.slice(1)} owner is already paired. Reinitiate pairing only if you want to replace this owner binding.`);
            } else {
                message.text(status.message || `${channel} pairing has not started yet.`);
            }

            $card.toggleClass('border-success', effectivelyPaired);
            $card.toggleClass('bg-success-subtle', effectivelyPaired);
            startBtn.text(effectivelyPaired ? 'Reinitiate Pairing' : 'Start Pairing');
            cancelBtn.toggleClass('d-none', state !== 'waiting');

            if (effectivelyPaired) {
                const parts = [];
                if (channel === 'telegram') {
                    parts.push('Telegram owner confirmed');
                    if (status.identity && status.identity.displayName) parts.push(status.identity.displayName);
                    if (status.identity && status.identity.id) {
                        parts.push(status.identity.id);
                    } else if (staged) {
                        parts.push(staged);
                    }
                } else {
                    parts.push('WhatsApp owner confirmed');
                    if (status.identity && status.identity.phone) parts.push(status.identity.phone);
                    if (status.identity && status.identity.jid) {
                        parts.push(status.identity.jid);
                    } else if (staged) {
                        parts.push(staged);
                    }
                }
                success.removeClass('d-none');
                successText.text(`${parts.join(' · ')}. You can continue to the next step, or reinitiate pairing if you need to replace it.`);
            } else {
                success.addClass('d-none');
            }

            this.renderPairingArtifact(channel, artifact, status);
            this.renderPairingIdentity(identity, channel, status);
        }

        renderPairingArtifact(channel, $artifact, status) {
            const artifacts = status && status.artifacts ? status.artifacts : {};
            const state = String(status && status.state ? status.state : '');
            const staged = channel === 'telegram' ? this.wizardData.UserTelegramID : this.wizardData.UserWhatsAppID;
            if (state === 'paired' || (state === 'not_started' && !!staged)) {
                $artifact.addClass('d-none').empty();
                return;
            }
            if (channel === 'telegram' && artifacts.code) {
                $artifact.removeClass('d-none').html(`
                    <div class="fw-semibold mb-1">One-time code</div>
                    <code class="fs-5">${escapeHtml(artifacts.code)}</code>
                    <div class="small text-muted mt-1">Send this exact code to the Telegram bot from the owner account.</div>
                `);
                return;
            }
            if (channel === 'whatsapp' && artifacts.qrCode) {
                $artifact.removeClass('d-none').html(`
                    <div class="fw-semibold mb-2">Scan this QR code</div>
                    <div class="bg-white rounded p-3 d-inline-block js-whatsapp-qr"></div>
                    <div class="small text-muted mt-2">${escapeHtml(artifacts.qrLabel || '')}</div>
                `);
                const container = $artifact.find('.js-whatsapp-qr').get(0);
                if (container && typeof window.QRCode !== 'undefined') {
                    container.innerHTML = '';
                    // eslint-disable-next-line no-new
                    new window.QRCode(container, {
                        text: artifacts.qrCode,
                        width: 220,
                        height: 220
                    });
                }
                return;
            }
            $artifact.addClass('d-none').empty();
        }

        renderPairingIdentity($identity, channel, status) {
            const identity = status && status.identity ? status.identity : null;
            const staged = channel === 'telegram' ? this.wizardData.UserTelegramID : this.wizardData.UserWhatsAppID;
            if (identity) {
                if (channel === 'telegram') {
                    const parts = [identity.displayName, identity.username ? `@${identity.username}` : '', identity.id].filter(Boolean);
                    $identity.html(`<span class="text-success"><i class="bi bi-check2-square me-1"></i>Paired owner: ${escapeHtml(parts.join(' · '))}</span>`);
                } else {
                    const parts = [identity.phone, identity.jid || identity.id].filter(Boolean);
                    $identity.html(`<span class="text-success"><i class="bi bi-check2-square me-1"></i>Paired owner: ${escapeHtml(parts.join(' · '))}</span>`);
                }
                return;
            }
            if (staged) {
                $identity.html(`<span class="text-success"><i class="bi bi-check2-square me-1"></i>Current paired owner: ${escapeHtml(String(staged))}</span>`);
                return;
            }
            $identity.text('');
        }

        initializePairingStep() {
            if (!this.currentStep || this.currentStep.id !== 'pairing') {
                return;
            }
            this.refreshAllPairings();
        }

        stopAllPairingPollers() {
            Object.values(this.pairingPollers).forEach((timer) => {
                if (timer) window.clearTimeout(timer);
            });
            this.pairingPollers = {};
        }

        async refreshAllPairings() {
            const channels = [];
            if (this.wizardData.TelegramEnabled) channels.push('telegram');
            if (this.wizardData.WhatsAppEnabled) channels.push('whatsapp');
            await Promise.all(channels.map((channel) => this.fetchPairingStatus(channel)));
            this.syncNav();
        }

        async refreshPairing(event) {
            const channel = $(event.currentTarget).closest('[data-pairing-channel]').data('pairing-channel');
            if (!channel) return;
            await this.fetchPairingStatus(channel);
            this.syncNav();
        }

        async startPairing(event) {
            const channel = $(event.currentTarget).closest('[data-pairing-channel]').data('pairing-channel');
            if (!channel) return;
            try {
                const resp = await fetch(`/setup/api/wizard/pairing/${encodeURIComponent(channel)}/start`, { method: 'POST' });
                const data = await resp.json();
                if (!data.success) throw new Error(data.message || `Failed to start ${channel} pairing`);
                this.applyPairingStatus(channel, data.data || {});
                this.schedulePairingPoll(channel, data.data || {});
                this.syncNav();
            } catch (err) {
                showAlert(this.$errorAlert, err.message || `Failed to start ${channel} pairing`);
            }
        }

        async cancelPairing(event) {
            const channel = $(event.currentTarget).closest('[data-pairing-channel]').data('pairing-channel');
            if (!channel) return;
            try {
                const resp = await fetch(`/setup/api/wizard/pairing/${encodeURIComponent(channel)}/cancel`, { method: 'POST' });
                const data = await resp.json();
                if (!data.success) throw new Error(data.message || `Failed to cancel ${channel} pairing`);
                this.applyPairingStatus(channel, data.data || {});
                this.schedulePairingPoll(channel, data.data || {});
                this.syncNav();
            } catch (err) {
                showAlert(this.$errorAlert, err.message || `Failed to cancel ${channel} pairing`);
            }
        }

        async fetchPairingStatus(channel) {
            try {
                const resp = await fetch(`/setup/api/wizard/pairing/${encodeURIComponent(channel)}/status`);
                const data = await resp.json();
                if (!data.success) throw new Error(data.message || `Failed to load ${channel} pairing status`);
                this.applyPairingStatus(channel, data.data || {});
                this.schedulePairingPoll(channel, data.data || {});
            } catch (err) {
                this.renderPairingError(channel, err.message || `Failed to load ${channel} pairing status`);
            }
        }

        schedulePairingPoll(channel, status) {
            if (this.pairingPollers[channel]) {
                window.clearTimeout(this.pairingPollers[channel]);
                delete this.pairingPollers[channel];
            }
            const state = status && status.state ? status.state : '';
            if (['paired', 'expired', 'failed', 'cancelled', 'not_started'].includes(state)) {
                return;
            }
            const delay = Number(status && status.pollAfterMs) > 0 ? Number(status.pollAfterMs) : 1500;
            this.pairingPollers[channel] = window.setTimeout(() => this.fetchPairingStatus(channel), delay);
        }

        applyPairingStatus(channel, status) {
            this.pairingStatus[channel] = status || {};
            if (channel === 'telegram' && status && status.identity && status.identity.id) {
                this.wizardData.UserTelegramID = status.identity.id;
            }
            if (channel === 'whatsapp' && status && status.identity && status.identity.id) {
                this.wizardData.UserWhatsAppID = status.identity.id;
            }
            this.renderPairingStatus(channel, status || {});
        }

        renderPairingError(channel, message) {
            const $card = this.$stepContent.find(`[data-pairing-channel="${channel}"]`);
            if (!$card.length) return;
            $card.find('.js-pairing-badge').attr('class', 'badge text-bg-danger js-pairing-badge').text('Error');
            $card.find('.js-pairing-message').text(message || 'Pairing failed.');
        }

        renderPairingStatus(channel, status) {
            const $card = this.$stepContent.find(`[data-pairing-channel="${channel}"]`);
            if (!$card.length) return;

            const state = String(status.state || 'not_started');
            const badge = $card.find('.js-pairing-badge');
            const message = $card.find('.js-pairing-message');
            const artifact = $card.find('.js-pairing-artifact');
            const identity = $card.find('.js-pairing-identity');
            const startBtn = $card.find('.js-pairing-start');
            const cancelBtn = $card.find('.js-pairing-cancel');

            const badgeMap = {
                not_started: ['text-bg-secondary', 'Not started'],
                waiting: ['text-bg-warning', 'Waiting'],
                paired: ['text-bg-success', 'Paired'],
                expired: ['text-bg-danger', 'Expired'],
                failed: ['text-bg-danger', 'Failed'],
                cancelled: ['text-bg-secondary', 'Cancelled']
            };
            const badgeInfo = badgeMap[state] || badgeMap.not_started;
            badge.attr('class', `badge ${badgeInfo[0]} js-pairing-badge`).text(badgeInfo[1]);
            message.text(status.message || `${channel} pairing has not started yet.`);

            startBtn.text(state === 'paired' ? 'Restart Pairing' : 'Start Pairing');
            cancelBtn.toggleClass('d-none', state !== 'waiting');

            this.renderPairingArtifact(channel, artifact, status);
            this.renderPairingIdentity(identity, channel, status);
        }

        renderPairingArtifact(channel, $artifact, status) {
            const artifacts = status && status.artifacts ? status.artifacts : {};
            if (channel === 'telegram' && artifacts.code) {
                $artifact.removeClass('d-none').html(`
                    <div class="fw-semibold mb-1">One-time code</div>
                    <code class="fs-5">${escapeHtml(artifacts.code)}</code>
                    <div class="small text-muted mt-1">Send this exact code to the Telegram bot from the owner account.</div>
                `);
                return;
            }
            if (channel === 'whatsapp' && artifacts.qrCode) {
                $artifact.removeClass('d-none').html(`
                    <div class="fw-semibold mb-2">Scan this QR code</div>
                    <div class="bg-white rounded p-3 d-inline-block js-whatsapp-qr"></div>
                    <div class="small text-muted mt-2">${escapeHtml(artifacts.qrLabel || '')}</div>
                `);
                const container = $artifact.find('.js-whatsapp-qr').get(0);
                if (container && typeof window.QRCode !== 'undefined') {
                    container.innerHTML = '';
                    // eslint-disable-next-line no-new
                    new window.QRCode(container, {
                        text: artifacts.qrCode,
                        width: 220,
                        height: 220
                    });
                }
                return;
            }
            $artifact.addClass('d-none').empty();
        }

        renderPairingIdentity($identity, channel, status) {
            const identity = status && status.identity ? status.identity : null;
            const staged = channel === 'telegram' ? this.wizardData.UserTelegramID : this.wizardData.UserWhatsAppID;
            if (identity) {
                if (channel === 'telegram') {
                    const parts = [identity.displayName, identity.username ? `@${identity.username}` : '', identity.id].filter(Boolean);
                    $identity.text(`Paired owner: ${parts.join(' · ')}`);
                } else {
                    const parts = [identity.phone, identity.jid || identity.id].filter(Boolean);
                    $identity.text(`Paired owner: ${parts.join(' · ')}`);
                }
                return;
            }
            if (staged) {
                $identity.text(`Currently staged owner ID: ${staged}`);
                return;
            }
            $identity.text('');
        }

        updateSliderDisplay($field) {
            if (!$field.hasClass('js-slider-field')) return;
            const fieldID = $field.attr('id');
            const unit = $field.data('unit') || '';
            const rawValue = String($field.val() ?? '');
            const formatted = rawValue.replace(/\.0$/, '');
            this.$formContent.find(`.js-slider-value[data-slider-for="${fieldID}"]`).text(unit ? `${formatted} ${unit}` : formatted);
        }

        async saveAll() {
            const dirtySections = Object.keys(this.dirtyState).filter(sectionId => this.dirtyState[sectionId]);
            const hasPendingOwnerPairings = Object.keys(this.pendingOwnerPairings).length > 0;
            if (!dirtySections.length && !hasPendingOwnerPairings) return;

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

                if (hasPendingOwnerPairings) {
                    const resp = await fetch('/setup/api/users/owner-pairing', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({
                            telegram_id: this.pendingOwnerPairings.telegram || '',
                            whatsapp_id: this.pendingOwnerPairings.whatsapp || ''
                        })
                    });
                    const data = await resp.json();
                    if (!data.success) {
                        throw new Error(data.message || 'Failed to save staged owner pairing identities');
                    }
                    this.pendingOwnerPairings = {};
                }

                this.applyPending = true;
                showAlert(this.$successAlert, dirtySections.length <= 1
                    ? 'Configuration saved to disk.'
                    : `Configuration saved to disk (${dirtySections.length} sections).`);
            } catch (err) {
                showAlert(this.$errorAlert, err.message || 'Failed to save configuration');
            } finally {
                this.saving = false;
                this.syncTopBar();
            }
        }

        async runFormAction(event) {
            const $button = $(event.currentTarget);
            const actionName = $button.data('action-name');
            const confirmText = $button.data('action-confirm');
            const label = $button.text().trim() || 'Action';
            if (!this.currentSection || this.currentSectionType === 'custom' || !actionName) return;
            if (confirmText && !window.confirm(confirmText)) return;

            showAlert(this.$errorAlert, '');
            showAlert(this.$successAlert, '');
            $button.prop('disabled', true);
            try {
                const resp = await fetch(`/setup/api/section-action/${encodeURIComponent(this.currentSection)}/${encodeURIComponent(actionName)}`, {
                    method: 'POST'
                });
                const data = await resp.json();
                if (!data.success) {
                    throw new Error(data.message || `Failed to run ${label}`);
                }
                await this.loadSection(this.currentSection);
                showAlert(this.$successAlert, data.message || `${label} completed.`);
            } catch (err) {
                showAlert(this.$errorAlert, err.message || `Failed to run ${label}`);
            } finally {
                $button.prop('disabled', false);
            }
        }

        async applySaved() {
            if (this.saving || !this.applyPending || this.hasAnyDirty()) return;
            showAlert(this.$errorAlert, '');
            showAlert(this.$successAlert, '');
            this.saving = true;
            this.syncTopBar();
            try {
                this.lastKnownInstanceID = await captureCurrentInstanceID();
                const applyResp = await fetch('/setup/api/apply', { method: 'POST' });
                const applyData = await applyResp.json();
                if (!applyData.success) {
                    throw new Error(applyData.message || 'Failed to apply configuration');
                }

                const apply = extractApplyResult(applyData);
                await this.handleApplyResult(apply, this.lastKnownInstanceID);
            } catch (err) {
                showAlert(this.$errorAlert, err.message || 'Failed to apply configuration');
            } finally {
                this.saving = false;
                this.syncTopBar();
            }
        }

        async handleApplyResult(apply, previousInstanceID) {
            const defaultMessage = 'Configuration applied.';
            if (!apply) {
                showAlert(this.$successAlert, defaultMessage);
                this.applyPending = false;
                this.syncTopBar();
                return;
            }

            if (apply.action === 'manual_restart') {
                this.$restartMessage.text(apply.message || 'Stop and restart the gateway process to apply changes.');
                this.$restartDetail.text('Waiting for the current GoClaw process to stop...');
                this.restartModal.show();
                await this.waitForGatewayAfterRestart(previousInstanceID);
                return;
            }

            if (apply.action === 'supervised_restart' && apply.waitForRestart) {
                this.$restartMessage.text(apply.message || 'Configuration saved. Waiting for GoClaw to restart...');
                this.$restartDetail.text('Waiting for the current GoClaw process to stop...');
                this.restartModal.show();
                await this.waitForGatewayAfterRestart(previousInstanceID);
                return;
            }

            showAlert(this.$successAlert, apply.message || defaultMessage);
            this.applyPending = false;
            this.syncTopBar();
        }

        async waitForGatewayAfterRestart(previousInstanceID) {
            const result = await waitForGatewayRestart(previousInstanceID, (update) => {
                if (update.phase === 'waiting_for_stop') {
                    this.$restartDetail.text('Waiting for the current GoClaw process to stop...');
                } else if (update.phase === 'waiting_for_start') {
                    this.$restartDetail.text('Waiting for GoClaw to come back online...');
                }
                if (update.elapsedMs > 10000) {
                    if (update.phase === 'waiting_for_stop') {
                        this.$restartDetail.text('Still waiting for GoClaw to stop...');
                    } else {
                        this.$restartDetail.text('Still waiting for GoClaw to restart...');
                    }
                }
            });
            this.restartModal.hide();
            if (result.ready) {
                this.lastKnownInstanceID = result.status && result.status.instanceID ? result.status.instanceID : this.lastKnownInstanceID;
                this.applyPending = false;
                this.syncTopBar();
                showAlert(this.$successAlert, 'GoClaw restarted and configuration is active.');
            } else {
                showAlert(this.$errorAlert, 'Configuration saved, but GoClaw did not come back online automatically. Restart it manually if needed.');
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
            this.pendingOwnerPairings = {};
            if (this.currentSection && this.currentSectionType !== 'custom') {
                await this.loadSection(this.currentSection);
            } else {
                this.syncTopBar();
            }
        }

        initModelWidgets() {
            this.$formContent.find('.js-model-chain').each((_, el) => {
                const fieldPath = $(el).data('field-path');
                const purpose = $(el).data('purpose') || '';
                this.ensureModelMetaForField(fieldPath, purpose);
                this.renderModelChain(fieldPath);
            });
        }

        renderSelectCustomWidgets() {
            this.$formContent.find('.js-select-custom').each((_, el) => {
                const $widget = $(el);
                const fieldPath = $widget.data('field-path');
                const currentValue = getByPath(this.formData, fieldPath);
                const currentText = currentValue == null ? '' : String(currentValue);
                const $select = $widget.find('.js-select-custom-select');
                const $inputWrap = $widget.find('.js-select-custom-input-wrap');
                const $input = $widget.find('.js-select-custom-input');
                const hasKnownOption = $select.find(`option[value="${CSS.escape(currentText)}"]`).length > 0;
                if (currentText && !hasKnownOption) {
                    $select.val('__custom__');
                    $input.val(currentText);
                    $inputWrap.removeClass('d-none');
                } else {
                    $select.val(hasKnownOption ? currentText : ($select.find('option').first().val() || ''));
                    $input.val('');
                    $inputWrap.toggleClass('d-none', $select.val() !== '__custom__');
                }
            });
        }

        handleSelectCustomChange(event) {
            const $select = $(event.currentTarget);
            const fieldPath = $select.data('field-path');
            const $widget = $select.closest('.js-select-custom');
            const $inputWrap = $widget.find('.js-select-custom-input-wrap');
            const $input = $widget.find('.js-select-custom-input');
            const selected = String($select.val() || '');
            if (selected === '__custom__') {
                $inputWrap.removeClass('d-none');
                setByPath(this.formData, fieldPath, String($input.val() || ''));
            } else {
                $inputWrap.addClass('d-none');
                setByPath(this.formData, fieldPath, selected);
            }
            this.sectionDrafts[this.currentSection] = deepClone(this.formData);
            this.dirtyState[this.currentSection] = this.currentDirty();
            this.fieldErrors[fieldPath] = '';
            this.applyShowWhen(this.$formContent, this.formData);
            this.renderFieldErrors(this.$formContent, this.fieldErrors);
            this.syncTopBar();
        }

        handleSelectCustomInput(event) {
            const $input = $(event.currentTarget);
            const fieldPath = $input.data('field-path');
            setByPath(this.formData, fieldPath, String($input.val() || ''));
            this.sectionDrafts[this.currentSection] = deepClone(this.formData);
            this.dirtyState[this.currentSection] = this.currentDirty();
            this.fieldErrors[fieldPath] = '';
            this.applyShowWhen(this.$formContent, this.formData);
            this.renderFieldErrors(this.$formContent, this.fieldErrors);
            this.syncTopBar();
        }

        ensureModelMetaForField(fieldPath, purpose) {
            const models = getByPath(this.formData, fieldPath) || [];
            models.forEach(modelRef => this.loadModelMeta(modelRef, purpose));
        }

        async loadModelMeta(modelRef, purpose = '') {
            if (this.mcMeta[modelRef] || this.mcLoading[modelRef]) return;
            const parts = String(modelRef).split('/');
            if (parts.length < 2) return;
            const alias = parts[0];
            const modelID = parts.slice(1).join('/');
            this.mcLoading[modelRef] = true;
            try {
                const query = purpose ? `?purpose=${encodeURIComponent(purpose)}` : '';
                const resp = await fetch(`/setup/api/models/${encodeURIComponent(alias)}${query}`);
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
                const isHugotEmbeddings = modelRef.startsWith('hugot-local/');
                html += `<li class="model-chain-item" draggable="true" data-mc-item="1" data-field-path="${escapeHtml(fieldPath)}" data-index="${index}">`;
                html += `<div class="model-chain-drag-handle"><i class="bi bi-grip-vertical"></i></div>`;
                html += `<div class="model-chain-content">`;
                html += `<div class="model-chain-header"><span class="model-chain-ref">${escapeHtml(modelRef)}</span>${index === 0 ? '<span class="badge bg-primary">Primary</span>' : ''}</div>`;
                if (meta) {
                    if (isHugotEmbeddings) {
                        html += `<div class="model-chain-meta"><span class="model-chain-name">${escapeHtml(meta.name || meta.id || modelRef)}</span> <span>Embeddings-only</span> <span>${escapeHtml(this.formatLocalModelState(meta))}</span></div>`;
                        html += `<div class="model-chain-caps">`;
                        html += `<span class="model-chain-cap model-chain-cap-ok"><i class="bi bi-check"></i> Recommended</span>`;
                        html += `<span class="model-chain-cap model-chain-cap-ok"><i class="bi bi-cpu"></i> Local</span>`;
                        html += `</div>`;
                    } else {
                        html += `<div class="model-chain-meta"><span class="model-chain-name">${escapeHtml(meta.name || meta.id || modelRef)}</span> <span>${escapeHtml(this.formatContext(meta.contextWindow))}</span> <span>${escapeHtml(this.formatCost(meta.cost))}</span></div>`;
                        html += `<div class="model-chain-caps">`;
                        html += this.renderCapabilityBadge(meta, 'vision', 'Vision');
                        html += this.renderCapabilityBadge(meta, 'toolUse', 'Tools');
                        html += this.renderCapabilityBadge(meta, 'reasoning', 'Reasoning');
                        html += `</div>`;
                    }
                    html += `<div class="model-chain-details${expanded ? '' : ' d-none'}">`;
                    html += `<dl class="row mb-0 small">`;
                    if (isHugotEmbeddings) {
                        html += `<dt class="col-4">Type</dt><dd class="col-8">Local embeddings model</dd>`;
                        html += `<dt class="col-4">Download</dt><dd class="col-8">${escapeHtml(this.formatLocalModelState(meta))}</dd>`;
                        html += `<dt class="col-4">Use Case</dt><dd class="col-8">Semantic search for memory, transcripts, and Memory Graph</dd>`;
                    } else {
                        html += `<dt class="col-4">Context</dt><dd class="col-8">${escapeHtml(((meta.contextWindow || 0).toLocaleString()) + ' tokens')}</dd>`;
                        html += `<dt class="col-4">Max Output</dt><dd class="col-8">${escapeHtml(((meta.maxOutputTokens || 0).toLocaleString()) + ' tokens')}</dd>`;
                        html += `<dt class="col-4">Cost (1M)</dt><dd class="col-8">$${escapeHtml(meta.cost && meta.cost.input ? meta.cost.input.toFixed(2) : '?')} / $${escapeHtml(meta.cost && meta.cost.output ? meta.cost.output.toFixed(2) : '?')}</dd>`;
                    }
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
            const purpose = $widget.data('purpose') || '';
            html += `<div class="model-chain-add js-model-chain-add" data-field-path="${escapeHtml(fieldPath)}" data-purpose="${escapeHtml(purpose)}"><i class="bi bi-plus-lg me-2"></i> Add Fallback Model</div>`;
            $widget.html(html);
        }

        renderCapabilityBadge(meta, capabilityKey, label) {
            const enabled = !!(meta.capabilities && meta.capabilities[capabilityKey]);
            return `<span class="model-chain-cap ${enabled ? 'model-chain-cap-ok' : 'model-chain-cap-warn'}"><i class="bi ${enabled ? 'bi-check' : 'bi-x'}"></i> ${escapeHtml(label)}</span>`;
        }

        formatLocalModelState(meta) {
            const name = (meta && meta.name) || '';
            return /\bcached\b/i.test(name) ? 'Cached locally' : 'Downloads on first use';
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

        async ensureProvidersLoaded(purpose = '') {
            const key = String(purpose || '').toLowerCase();
            if (this.mcProvidersLoadedByPurpose[key]) {
                this.mcProviders = this.mcProvidersByPurpose[key] || [];
                return;
            }
            try {
                const query = key ? `?purpose=${encodeURIComponent(key)}` : '';
                const resp = await fetch(`/setup/api/providers${query}`);
                const data = await resp.json();
                if (data.success) {
                    const providers = data.data.providers || [];
                    this.mcProvidersByPurpose[key] = providers;
                    this.mcProvidersLoadedByPurpose[key] = true;
                    this.mcProviders = providers;
                }
            } catch (_err) {
                this.mcProviders = [];
            }
        }

        async openModelModal(fieldPath, purpose = '') {
            this.mcModalField = fieldPath;
            this.mcModalPurpose = String(purpose || '').toLowerCase();
            this.mcModalStep = 1;
            this.mcSelectedProvider = null;
            this.mcSelectedModel = null;
            this.mcManualModelID = '';
            this.mcAvailableModels = [];
            this.mcModelSearch = '';
            $('#mcModelSearch').val('');
            $('#mcManualModelID').val('');
            await this.ensureProvidersLoaded(this.mcModalPurpose);
            this.renderModelModal();
            this.mcModal.show();
        }

        renderModelModal() {
            const hasManualModel = !!this.mcManualModelID;
            $('#mcModalTitle').text(this.mcModalStep === 1 ? 'Select Provider' : 'Select Model');
            $('#mcModalStepProviders').toggleClass('d-none', this.mcModalStep !== 1);
            $('#mcModalStepModels').toggleClass('d-none', this.mcModalStep !== 2);
            $('#mcModalBack').toggleClass('d-none', this.mcModalStep !== 2);
            $('#mcModalAdd').toggleClass('d-none', this.mcModalStep !== 2).prop('disabled', !this.mcSelectedModel && !hasManualModel);

            if (this.mcModalStep === 1) {
                const items = this.mcProviders.map(provider => (
                    `<button type="button" class="list-group-item list-group-item-action d-flex justify-content-between align-items-center js-model-provider-select" data-provider-alias="${escapeHtml(provider.alias)}">` +
                    `<div><div class="fw-bold">${escapeHtml(provider.alias)}</div><small class="text-muted">${escapeHtml((provider.name || provider.alias) + ' · ' + provider.driver + (provider.modelCount ? ' · ' + provider.modelCount + ' models' : ''))}</small></div>` +
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
                if (this.mcManualModelID) {
                    return `<div>
                        <h6 class="mb-3">${escapeHtml(this.mcManualModelID)}</h6>
                        <div class="alert alert-info py-2 px-3 mb-0 small">
                            Manual model ID mode. GoClaw will save this model reference exactly as entered.
                        </div>
                    </div>`;
                }
                return '<div class="text-center text-muted py-5">Select a model to see details</div>';
            }
            const isHugotEmbeddings = !!(this.mcSelectedProvider && this.mcSelectedProvider.alias === 'hugot-local');
            if (isHugotEmbeddings) {
                return `<div>
                    <h6 class="mb-3">${escapeHtml(model.name || model.id)}</h6>
                    <dl class="row small">
                        <dt class="col-5">Type</dt><dd class="col-7">Local embeddings model</dd>
                        <dt class="col-5">Provider</dt><dd class="col-7">Hugot</dd>
                        <dt class="col-5">Download</dt><dd class="col-7">${escapeHtml(this.formatLocalModelState(model))}</dd>
                    </dl>
                    <h6 class="mt-3 mb-2">Use Case</h6>
                    <div class="d-flex flex-wrap gap-1">
                        <span class="badge bg-success"><i class="bi bi-check"></i> Embeddings-only</span>
                        <span class="badge bg-success"><i class="bi bi-check"></i> Recommended</span>
                        <span class="badge bg-secondary"><i class="bi bi-cpu"></i> Local</span>
                    </div>
                    <div class="mt-3"><small class="text-muted">Used for semantic search in memory, transcripts, and Memory Graph.</small></div>
                </div>`;
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
            this.mcManualModelID = '';
            $('#mcManualModelID').val('');
            this.renderModelModal();
        }

        async selectModelProvider(alias) {
            const provider = this.mcProviders.find(item => item.alias === alias);
            if (!provider) return;
            this.mcSelectedProvider = provider;
            this.mcSelectedModel = null;
            this.mcManualModelID = '';
            $('#mcManualModelID').val('');
            this.mcModalStep = 2;
            $('#mcModelLoading').removeClass('d-none');
            $('#mcModelEmpty').addClass('d-none');
            $('#mcModelList').empty();
            $('#mcModelDetails').html('<div class="text-center text-muted py-5">Select a model to see details</div>');
            this.renderModelModal();

            try {
                const query = this.mcModalPurpose ? `?purpose=${encodeURIComponent(this.mcModalPurpose)}` : '';
                const resp = await fetch(`/setup/api/models/${encodeURIComponent(alias)}${query}`);
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
            this.mcManualModelID = '';
            $('#mcManualModelID').val('');
            this.renderModelModal();
        }

        addSelectedModelToChain() {
            if (!this.mcSelectedProvider || !this.mcModalField) return;
            const manualModelID = (this.mcManualModelID || '').trim();
            const selectedModelID = this.mcSelectedModel ? this.mcSelectedModel.id : '';
            const modelID = manualModelID || selectedModelID;
            if (!modelID) return;

            const modelRef = `${this.mcSelectedProvider.alias}/${modelID}`;
            const models = [...(getByPath(this.formData, this.mcModalField) || [])];
            if (models.includes(modelRef)) {
                window.alert('This model is already in the chain.');
                return;
            }
            models.push(modelRef);
            setByPath(this.formData, this.mcModalField, models);
            if (this.mcSelectedModel && this.mcSelectedModel.id === modelID) {
                this.mcMeta[modelRef] = this.mcSelectedModel;
            }
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

        async ensureProviderDriverOptions() {
            if (this.providerDriverOptionsLoaded) return;
            try {
                const resp = await fetch('/setup/api/drivers');
                const data = await resp.json();
                if (data.success && data.data && Array.isArray(data.data.drivers)) {
                    this.providerDriverOptions = data.data.drivers.map(item => ({
                        value: item.id,
                        label: item.label || item.id
                    }));
                }
                this.providerDriverOptionsLoaded = true;
            } catch (_err) {
                this.providerDriverOptions = [...PROVIDER_DRIVER_OPTIONS];
                this.providerDriverOptionsLoaded = true;
            }
        }

        renderProviderLists() {
            Promise.all([
                this.ensureProviderPresets(),
                this.ensureProviderDriverOptions()
            ]).then(() => {
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
                const baseURL = cfg.baseURL || '';
                const managedLocal = cfg.driver === 'llamacpp' && cfg.llamacpp && cfg.llamacpp.mode === 'managed';
                html += `<div class="provider-item${expanded ? ' provider-item-expanded' : ''}">`;
                html += `<div class="provider-header"><div class="provider-info"><span class="provider-alias">${escapeHtml(alias)}</span>`;
                html += `<span class="provider-meta"><span>${escapeHtml(this.providerPresetName(cfg))}</span><span class="provider-key">${escapeHtml(this.maskApiKey(cfg.apiKey))}</span>${cfg.promptCaching ? '<span class="badge bg-info">Cache</span>' : ''}${managedLocal ? '<span class="badge bg-secondary">managed local</span>' : ''}</span></div>`;
                html += `<div class="provider-actions"><button type="button" class="provider-btn js-provider-toggle" data-field-path="${escapeHtml(fieldPath)}" data-alias="${escapeHtml(alias)}"><i class="bi ${expanded ? 'bi-chevron-up' : 'bi-chevron-down'}"></i></button>`;
                if (managedLocal) {
                    html += `<button type="button" class="provider-btn js-provider-open-local-llm" title="Open Local LLM"><i class="bi bi-cpu"></i></button>`;
                } else {
                    html += `<button type="button" class="provider-btn provider-btn-remove js-provider-delete" data-field-path="${escapeHtml(fieldPath)}" data-alias="${escapeHtml(alias)}"><i class="bi bi-x-lg"></i></button>`;
                }
                html += `</div></div>`;
                if (expanded) {
                    html += `<div class="provider-form"><div class="row g-3">`;
                    if (managedLocal) {
                        html += `<div class="col-12"><div class="alert alert-light border mb-0">This provider is backed by the managed local llama.cpp runtime. Use the <button type="button" class="btn btn-link btn-sm p-0 align-baseline js-provider-open-local-llm">Local LLM</button> section for model downloads, start/stop, and runtime status.</div></div>`;
                    }
                    html += `<div class="col-md-6"><label class="form-label">Alias</label><input type="text" class="form-control form-control-sm" value="${escapeHtml(alias)}" disabled></div>`;
                    html += `<div class="col-md-6"><label class="form-label">Driver</label><select class="form-select form-select-sm js-provider-input" data-field-path="${escapeHtml(fieldPath)}" data-alias="${escapeHtml(alias)}" data-provider-field="driver"${managedLocal ? ' disabled' : ''}>${this.renderOptions(this.providerDriverOptions, cfg.driver || '')}</select></div>`;
                    html += `<div class="col-12"><label class="form-label">API Key (optional for local/self-hosted providers)</label><div class="input-group input-group-sm"><input type="${showKey ? 'text' : 'password'}" class="form-control js-provider-input" data-field-path="${escapeHtml(fieldPath)}" data-alias="${escapeHtml(alias)}" data-provider-field="apiKey" value="${escapeHtml(cfg.apiKey || '')}" placeholder="Leave empty if your provider does not require one"${managedLocal ? ' disabled' : ''}><button type="button" class="btn btn-outline-secondary js-provider-toggle-key" data-field-path="${escapeHtml(fieldPath)}" data-alias="${escapeHtml(alias)}"${managedLocal ? ' disabled' : ''}><i class="bi ${showKey ? 'bi-eye-slash' : 'bi-eye'}"></i></button></div></div>`;
                    html += `<div class="col-12"><label class="form-label">Base URL (optional)</label><input type="text" class="form-control form-control-sm js-provider-input" data-field-path="${escapeHtml(fieldPath)}" data-alias="${escapeHtml(alias)}" data-provider-field="baseURL" value="${escapeHtml(baseURL)}" placeholder="Leave empty for default"${managedLocal ? ' disabled' : ''}></div>`;
                    html += `<div class="col-md-6"><div class="form-check form-switch"><input class="form-check-input js-provider-input" type="checkbox" data-field-path="${escapeHtml(fieldPath)}" data-alias="${escapeHtml(alias)}" data-provider-field="promptCaching"${cfg.promptCaching ? ' checked' : ''}${managedLocal ? ' disabled' : ''}><label class="form-check-label">Prompt Caching</label></div></div>`;
                    html += `<div class="col-md-6"><label class="form-label">Thinking Level</label><select class="form-select form-select-sm js-provider-input" data-field-path="${escapeHtml(fieldPath)}" data-alias="${escapeHtml(alias)}" data-provider-field="thinkingLevel"${managedLocal ? ' disabled' : ''}>${this.renderOptions(THINKING_LEVEL_OPTIONS, cfg.thinkingLevel || '')}</select></div>`;
                    html += `<div class="col-md-6"><label class="form-label">Max Tokens</label><input type="number" class="form-control form-control-sm js-provider-input" data-field-path="${escapeHtml(fieldPath)}" data-alias="${escapeHtml(alias)}" data-provider-field="maxTokens" value="${escapeHtml(cfg.maxTokens || '')}" placeholder="0 = default"${managedLocal ? ' disabled' : ''}></div>`;
                    html += `<div class="col-md-6"><label class="form-label">Context Window (tokens)</label><input type="number" class="form-control form-control-sm js-provider-input" data-field-path="${escapeHtml(fieldPath)}" data-alias="${escapeHtml(alias)}" data-provider-field="contextTokens" value="${escapeHtml(cfg.contextTokens || '')}" placeholder="0 = auto-detect"${managedLocal ? ' disabled' : ''}></div>`;
                    html += `<div class="col-md-6"><label class="form-label">Timeout (seconds)</label><input type="number" class="form-control form-control-sm js-provider-input" data-field-path="${escapeHtml(fieldPath)}" data-alias="${escapeHtml(alias)}" data-provider-field="timeoutSeconds" value="${escapeHtml(cfg.timeoutSeconds || '')}" placeholder="0 = default"${managedLocal ? ' disabled' : ''}></div>`;
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
                    html += `<div class="mb-3"><label class="form-label">API Key (optional for local/self-hosted providers)</label><input type="password" class="form-control form-control-sm js-provider-input" data-field-path="${escapeHtml(fieldPath)}" data-provider-field="newApiKey" value="${escapeHtml(cfg.apiKey || '')}" placeholder="Leave empty if your provider does not require one"></div>`;
                    if (preset.driver === 'anthropic') {
                        html += `<div class="mb-3"><div class="form-check form-switch"><input class="form-check-input js-provider-input" type="checkbox" data-field-path="${escapeHtml(fieldPath)}" data-provider-field="newPromptCaching"${cfg.promptCaching ? ' checked' : ''}><label class="form-check-label">Enable Prompt Caching</label></div></div>`;
                    }
                    html += `<button type="button" class="btn btn-primary btn-sm js-provider-save-new" data-field-path="${escapeHtml(fieldPath)}"${!ui.newAlias ? ' disabled' : ''}><i class="bi bi-plus-lg me-1"></i> Add Provider</button></div>`;
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
                baseURL: preset.apiEndpoint || '',
                promptCaching: preset.driver === 'anthropic',
                llamacpp: preset.llamacpp ? deepClone(preset.llamacpp) : undefined
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
            if (providerField === 'maxTokens' || providerField === 'contextTokens' || providerField === 'timeoutSeconds') {
                value = value === '' ? 0 : Number(value);
            }
            if (providerField === 'baseURL') {
                providers[alias].baseURL = value;
            } else if (providerField === 'driver') {
                providers[alias].driver = value;
            } else {
                providers[alias][providerField] = value;
            }
            setByPath(this.formData, fieldPath, providers);
            this.markCurrentSectionDirty();
            this.mcProvidersByPurpose = {};
            this.mcProvidersLoadedByPurpose = {};
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

            providers[ui.newAlias] = cfg;
            setByPath(this.formData, fieldPath, providers);
            ui.expanded[ui.newAlias] = true;
            this.markCurrentSectionDirty();
            this.mcProvidersByPurpose = {};
            this.mcProvidersLoadedByPurpose = {};
            this.cancelAddProvider(fieldPath);
        }

        deleteProvider(fieldPath, alias) {
            if (!window.confirm(`Delete provider "${alias}"? This cannot be undone.`)) return;
            const providers = deepClone(getByPath(this.formData, fieldPath) || {});
            delete providers[alias];
            setByPath(this.formData, fieldPath, providers);
            this.markCurrentSectionDirty();
            this.mcProvidersByPurpose = {};
            this.mcProvidersLoadedByPurpose = {};
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

        stopLocalLLMPolling() {
            if (this.localLLMPollTimer) {
                window.clearTimeout(this.localLLMPollTimer);
                this.localLLMPollTimer = null;
            }
        }

        scheduleLocalLLMPoll(delayMs) {
            this.stopLocalLLMPolling();
            if (this.currentSection !== 'local-llm') return;
            const delay = Math.max(500, Number(delayMs || 1000));
            this.localLLMPollTimer = window.setTimeout(() => {
                this.loadLocalLLM({ silent: true });
            }, delay);
        }

        async loadLocalLLM(options = {}) {
            if (!options.silent) {
                this.localLLMError = '';
                this.renderLocalLLMSection();
            }
            try {
                const resp = await fetch('/setup/api/local-llm', { cache: 'no-store' });
                const data = await resp.json();
                if (!data.success) throw new Error(data.message || 'Failed to load local LLM state');
                this.localLLMState = data.data || {};
                this.localLLMError = '';
            } catch (err) {
                this.localLLMError = err.message || 'Failed to load local LLM state';
            } finally {
                this.renderLocalLLMSection();
                const activeJobs = ((this.localLLMState && this.localLLMState.jobs) || []).filter(job => job && job.state === 'running');
                if (activeJobs.length) {
                    const pollMs = activeJobs.reduce((min, job) => {
                        const next = Number(job.pollAfterMs || 1000);
                        return Math.min(min, next > 0 ? next : 1000);
                    }, 1000);
                    this.scheduleLocalLLMPoll(pollMs);
                } else {
                    this.stopLocalLLMPolling();
                }
            }
        }

        async runLocalLLMAction(action, extra = {}) {
            if (this.localLLMActionPending) return;
            this.localLLMActionPending = true;
            this.localLLMError = '';
            this.renderLocalLLMSection();
            try {
                const resp = await fetch('/setup/api/local-llm', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ action, ...extra })
                });
                const data = await resp.json();
                if (!data.success) throw new Error(data.message || 'Local LLM action failed');
                this.syncLocalLLMConfigCaches(data.data || {});
                await this.loadLocalLLM({ silent: true });
                showAlert(this.$successAlert, data.message || 'Local LLM action started');
            } catch (err) {
                this.localLLMError = err.message || 'Local LLM action failed';
                this.renderLocalLLMSection();
            } finally {
                this.localLLMActionPending = false;
                this.renderLocalLLMSection();
            }
        }

        syncLocalLLMConfigCaches(data) {
            if (!data || !data.configUpdated) return;
            this.syncManagedProviderSectionDraft('llm-providers', data.providerAlias, data.providerConfig);
            this.syncManagedAgentChainDraft('llm', data.providerAlias, data.agentModelRef);
            this.mcProvidersByPurpose = {};
            this.mcProvidersLoadedByPurpose = {};
        }

        syncManagedProviderSectionDraft(sectionId, alias, providerConfig) {
            if (!alias || !providerConfig) return;
            const applyUpdate = (sectionData) => {
                if (!sectionData || typeof sectionData !== 'object' || Array.isArray(sectionData)) return;
                if (!sectionData.providers || typeof sectionData.providers !== 'object' || Array.isArray(sectionData.providers)) {
                    sectionData.providers = {};
                }
                sectionData.providers[alias] = deepClone(providerConfig);
            };
            applyUpdate(this.sectionDrafts[sectionId]);
            applyUpdate(this.sectionOriginals[sectionId]);
        }

        syncManagedAgentChainDraft(sectionId, alias, agentModelRef) {
            if (!alias || !agentModelRef) return;
            const aliasPrefix = `${alias}/`;
            const applyUpdate = (sectionData) => {
                if (!sectionData || typeof sectionData !== 'object' || Array.isArray(sectionData)) return;
                if (!sectionData.agent || typeof sectionData.agent !== 'object' || Array.isArray(sectionData.agent)) {
                    sectionData.agent = {};
                }
                const models = Array.isArray(sectionData.agent.models) ? [...sectionData.agent.models] : [];
                const existingIndex = models.findIndex(item => String(item || '').startsWith(aliasPrefix));
                if (existingIndex >= 0) {
                    models[existingIndex] = agentModelRef;
                } else {
                    models.push(agentModelRef);
                }
                sectionData.agent.models = models;
            };
            applyUpdate(this.sectionDrafts[sectionId]);
            applyUpdate(this.sectionOriginals[sectionId]);
        }

        localLLMStatusBadgeClass(status) {
            const state = (((status || {}).server || {}).state || '').toLowerCase();
            if (state === 'running' && status.server && status.server.healthy) return 'text-bg-success';
            if (state === 'running') return 'text-bg-warning';
            if ((status || {}).lastError) return 'text-bg-danger';
            return 'text-bg-secondary';
        }

        localLLMJobBadgeClass(job) {
            const state = String((job || {}).state || '').toLowerCase();
            if (state === 'completed') return 'text-bg-success';
            if (state === 'failed') return 'text-bg-danger';
            if (state === 'canceled') return 'text-bg-secondary';
            return 'text-bg-warning';
        }

        renderLocalLLMSection() {
            const data = this.localLLMState || {};
            const status = data.status || {};
            const server = status.server || {};
            const models = Array.isArray(data.models) ? data.models : [];
            const jobs = Array.isArray(data.jobs) ? data.jobs : [];
            const providers = Array.isArray(data.managedProviders) ? data.managedProviders : [];
            const activeJobs = jobs.filter(job => job && job.state === 'running');
            const systemProfile = status.systemProfile || ((data.recommendations || {}).profile || {});
            const summary = (data.recommendations || {}).summary || '';
            const runtimeVersion = data.runtimeVersion || {};
            const wiring = data.wiring || {};
            const selectedModelID = status.modelID || ((data.defaultSpec || {}).modelID || '');
            const defaultProviderAlias = wiring.defaultProvider || data.defaultProvider || '';
            const providerInAgentChain = !!wiring.providerInAgentChain;
            const agentModelRef = wiring.agentModelRef || '';
            const recommendedAlias = wiring.recommendedAlias || 'local-llm';
            const defaultManagedProvider = providers.find(item => item.alias === defaultProviderAlias) || providers[0] || null;
            const providerModelID = defaultManagedProvider ? (defaultManagedProvider.managedModelID || '') : '';
            const managedAgentModelRef = defaultProviderAlias ? `${defaultProviderAlias}/managed` : '';
            const providerUsesManagedRef = !!managedAgentModelRef && agentModelRef === managedAgentModelRef;
            const serverSummary = server.endpoint ? escapeHtml(server.endpoint) : 'Not started';
            const runtimeModeText = runtimeVersion.usingLatestByDefault
                ? 'Latest by default'
                : 'Pinned by managed provider config';
            const runtimeNote = runtimeVersion.usingLatestByDefault
                ? 'Ensure Runtime will resolve the latest supported llama.cpp builder release.'
                : 'Ensure Runtime follows the managed provider pin unless you explicitly choose Ensure Latest.';

            const providerSummary = providers.length
                ? providers.map(item => `<div class="small mb-2"><div><code>${escapeHtml(item.alias)}</code>${item.isAgentDefault ? ' <span class="badge text-bg-primary">agent default</span>' : ''}</div><div class="text-muted">managed model: <code>${escapeHtml(item.managedModelID || '-')}</code></div></div>`).join('')
                : '<div class="text-muted small">No managed `llamacpp` provider is configured yet. The runtime can still be prepared here, but the agent will not use it until a managed provider is configured.</div>';
            const providerActions = [];
            if (!providers.length) {
                providerActions.push(`<button type="button" class="btn btn-sm btn-outline-primary js-local-llm-action" data-action="configure_managed_provider" data-model-id="${escapeHtml(selectedModelID || '')}"${this.localLLMActionPending ? ' disabled' : ''}>Add Managed Provider</button>`);
                providerActions.push(`<button type="button" class="btn btn-sm btn-primary js-local-llm-action" data-action="use_for_agent" data-model-id="${escapeHtml(selectedModelID || '')}"${this.localLLMActionPending ? ' disabled' : ''}>Add Provider + Add To Agent</button>`);
            } else {
                if (!providerInAgentChain || !providerUsesManagedRef) {
                    providerActions.push(`<button type="button" class="btn btn-sm btn-outline-primary js-local-llm-action" data-action="add_managed_provider_to_agent_chain"${this.localLLMActionPending ? ' disabled' : ''}>Add To Agent Chain</button>`);
                }
                if (selectedModelID && providerModelID !== selectedModelID) {
                    providerActions.push(`<button type="button" class="btn btn-sm btn-primary js-local-llm-action" data-action="use_for_agent" data-model-id="${escapeHtml(selectedModelID)}"${this.localLLMActionPending ? ' disabled' : ''}>Use Selected Model For Agent</button>`);
                }
            }
            const providerFooter = !providers.length
                ? `<div class="small text-muted mt-3">Suggested alias: <code>${escapeHtml(recommendedAlias)}</code>${selectedModelID ? ` · selected model: <code>${escapeHtml(selectedModelID)}</code>` : ''}</div>`
                : `<div class="small text-muted mt-3">${agentModelRef ? `Agent chain ref: <code>${escapeHtml(agentModelRef)}</code>${providerUsesManagedRef ? '' : ' (will be normalized to <code>' + escapeHtml(managedAgentModelRef || 'alias/managed') + '</code>)'}` : 'This managed provider is not yet in the agent chain.'}</div>`;

            const jobsHtml = activeJobs.length
                ? activeJobs.map(job => `
                    <div class="card mb-2">
                        <div class="card-body">
                            <div class="d-flex justify-content-between align-items-start gap-3">
                                <div>
                                    <div class="fw-semibold">${escapeHtml(job.ownerAction || 'job')}</div>
                                    <div class="small text-muted">${escapeHtml(job.phase || '')}</div>
                                    <div>${escapeHtml(job.message || '')}</div>
                                </div>
                                <div class="text-end">
                                    <div><span class="badge ${this.localLLMJobBadgeClass(job)}">${escapeHtml(job.state)}</span></div>
                                    <button type="button" class="btn btn-sm btn-outline-danger mt-2 js-local-llm-action" data-action="cancel_job" data-job-id="${escapeHtml(job.jobID)}"${this.localLLMActionPending ? ' disabled' : ''}>Cancel</button>
                                </div>
                            </div>
                            <div class="progress mt-3" role="progressbar" aria-valuemin="0" aria-valuemax="100" aria-valuenow="${escapeHtml(String(job.progressPercent || 0))}">
                                <div class="progress-bar progress-bar-striped progress-bar-animated" style="width: ${escapeHtml(String(job.progressPercent || 0))}%">${escapeHtml(String(job.progressPercent || 0))}%</div>
                            </div>
                        </div>
                    </div>
                `).join('')
                : '<div class="text-muted small">No active local LLM jobs.</div>';

            const modelCards = models.map(model => {
                const modelUsedByAgent = !!defaultManagedProvider && providerUsesManagedRef && providerModelID === model.id;
                const badges = [
                    model.installed ? '<span class="badge text-bg-success">installed</span>' : '<span class="badge text-bg-secondary">not installed</span>',
                    model.selected ? '<span class="badge text-bg-primary">selected</span>' : '',
                    modelUsedByAgent ? '<span class="badge text-bg-dark">agent wired</span>' : '',
                    model.recommended ? '<span class="badge text-bg-info">recommended</span>' : '',
                    model.defaultSelected ? '<span class="badge text-bg-light">default</span>' : '',
                    model.viable ? '' : '<span class="badge text-bg-warning">heavy</span>'
                ].filter(Boolean).join(' ');
                return `
                    <div class="card mb-3">
                        <div class="card-body">
                            <div class="d-flex justify-content-between align-items-start gap-3 flex-wrap">
                                <div>
                                    <div class="fw-semibold">${escapeHtml(model.label)}</div>
                                    <div class="small text-muted">${escapeHtml(model.hfRepo || '')}</div>
                                </div>
                                <div>${badges}</div>
                            </div>
                            <p class="small text-muted mt-2 mb-3">${escapeHtml(model.reason || '')}</p>
                            <div class="row g-3 small mb-3">
                                <div class="col-12 col-md-4"><div class="text-muted">Download</div><div>${escapeHtml(formatBytes(model.approxDownloadBytes))}</div></div>
                                <div class="col-12 col-md-4"><div class="text-muted">Recommended RAM</div><div>${escapeHtml(formatBytes(model.recommendedMinRAMBytes))}</div></div>
                                <div class="col-12 col-md-4"><div class="text-muted">Quant</div><div>${escapeHtml(model.preferredQuant || '-')}</div></div>
                            </div>
                            <div class="d-flex gap-2 flex-wrap">
                                <button type="button" class="btn btn-sm btn-outline-primary js-local-llm-action" data-action="download_model" data-model-id="${escapeHtml(model.id)}"${this.localLLMActionPending ? ' disabled' : ''}>${model.installed ? 'Re-download' : 'Download'}</button>
                                <button type="button" class="btn btn-sm btn-outline-secondary js-local-llm-action" data-action="select_model" data-model-id="${escapeHtml(model.id)}"${this.localLLMActionPending || model.selected ? ' disabled' : ''}>Select</button>
                                <button type="button" class="btn btn-sm btn-primary js-local-llm-action" data-action="start" data-model-id="${escapeHtml(model.id)}"${this.localLLMActionPending ? ' disabled' : ''}>Start With This Model</button>
                                <button type="button" class="btn btn-sm btn-outline-dark js-local-llm-action" data-action="use_for_agent" data-model-id="${escapeHtml(model.id)}"${this.localLLMActionPending || !model.installed || modelUsedByAgent ? ' disabled' : ''}>${modelUsedByAgent ? 'Already In Agent Flow' : 'Use For Agent'}</button>
                            </div>
                        </div>
                    </div>
                `;
            }).join('');

            this.$usersContent.html(`
                <div class="row g-3 mb-4">
                    <div class="col-12 col-xl-4">
                        <div class="card h-100">
                            <div class="card-header d-flex justify-content-between align-items-center">
                                <span>Runtime Status</span>
                                <span class="badge ${this.localLLMStatusBadgeClass(status)}">${escapeHtml(server.state || 'stopped')}</span>
                            </div>
                            <div class="card-body">
                                <dl class="row mb-0 small">
                                    <dt class="col-5">Endpoint</dt><dd class="col-7">${serverSummary}</dd>
                                    <dt class="col-5">Model</dt><dd class="col-7">${escapeHtml(selectedModelID || '-')}</dd>
                                    <dt class="col-5">Runtime</dt><dd class="col-7">${escapeHtml(status.runtimeVersion || '-')}</dd>
                                    <dt class="col-5">Backend</dt><dd class="col-7">${escapeHtml(status.backend || '-')}${server.healthy ? ' <span class="badge text-bg-success">healthy</span>' : ''}</dd>
                                    <dt class="col-5">PID</dt><dd class="col-7">${escapeHtml(String(server.pid || '-'))}</dd>
                                </dl>
                                ${status.lastError ? `<div class="alert alert-danger mt-3 mb-0">${escapeHtml(status.lastError)}</div>` : ''}
                            </div>
                        </div>
                    </div>
                    <div class="col-12 col-xl-4">
                        <div class="card h-100">
                            <div class="card-header">Managed Providers</div>
                            <div class="card-body">
                                ${providerSummary}
                                ${providerActions.length ? `<div class="d-flex gap-2 flex-wrap mt-3">${providerActions.join('')}</div>` : ''}
                                ${providerFooter}
                            </div>
                        </div>
                    </div>
                    <div class="col-12 col-xl-4">
                        <div class="card h-100">
                            <div class="card-header">Machine Profile</div>
                            <div class="card-body">
                                <div class="small text-muted mb-2">${escapeHtml(summary || 'No recommendation summary available')}</div>
                                <dl class="row mb-0 small">
                                    <dt class="col-5">Platform</dt><dd class="col-7">${escapeHtml(`${systemProfile.osFlavor || '-'}/${systemProfile.arch || '-'}`)}</dd>
                                    <dt class="col-5">Backend</dt><dd class="col-7">${escapeHtml(systemProfile.recommended || '-')}</dd>
                                    <dt class="col-5">RAM</dt><dd class="col-7">${escapeHtml(formatBytes(systemProfile.totalRAMBytes))}</dd>
                                </dl>
                            </div>
                        </div>
                    </div>
                </div>

                <div class="card mb-4">
                    <div class="card-header">Runtime Version</div>
                    <div class="card-body">
                        <div class="small text-muted mb-2">${escapeHtml(runtimeNote)}</div>
                        <dl class="row mb-0 small">
                            <dt class="col-4">Mode</dt><dd class="col-8">${escapeHtml(runtimeModeText)}</dd>
                            <dt class="col-4">Installed</dt><dd class="col-8">${escapeHtml(runtimeVersion.installed || '-')}</dd>
                            <dt class="col-4">Configured</dt><dd class="col-8">${escapeHtml(runtimeVersion.configured || 'latest')}</dd>
                            <dt class="col-4">Latest</dt><dd class="col-8">${escapeHtml(runtimeVersion.latest || '-')}</dd>
                            <dt class="col-4">Effective</dt><dd class="col-8">${escapeHtml(runtimeVersion.effective || '-')}</dd>
                        </dl>
                        ${runtimeVersion.latestLookupError ? `<div class="alert alert-warning mt-3 mb-0">${escapeHtml(runtimeVersion.latestLookupError)}</div>` : ''}
                    </div>
                </div>

                <div class="card mb-4">
                    <div class="card-header d-flex justify-content-between align-items-center">
                        <span>Actions</span>
                        <div class="d-flex gap-2 flex-wrap">
                            <button type="button" class="btn btn-sm btn-outline-secondary js-local-llm-refresh"${this.localLLMActionPending ? ' disabled' : ''}>Refresh</button>
                            <button type="button" class="btn btn-sm btn-outline-primary js-local-llm-action" data-action="ensure_runtime"${this.localLLMActionPending ? ' disabled' : ''}>Ensure Runtime</button>
                            <button type="button" class="btn btn-sm btn-outline-primary js-local-llm-action" data-action="ensure_latest_runtime" data-runtime-version="${escapeHtml(runtimeVersion.latest || '')}"${this.localLLMActionPending || !runtimeVersion.latest ? ' disabled' : ''}>Ensure Latest</button>
                            <button type="button" class="btn btn-sm btn-primary js-local-llm-action" data-action="start"${this.localLLMActionPending ? ' disabled' : ''}>Start Server</button>
                            <button type="button" class="btn btn-sm btn-outline-danger js-local-llm-action" data-action="stop"${this.localLLMActionPending || !server.pid ? ' disabled' : ''}>Stop Server</button>
                        </div>
                    </div>
                    <div class="card-body">
                        ${this.localLLMError ? `<div class="alert alert-danger mb-3">${escapeHtml(this.localLLMError)}</div>` : ''}
                        ${jobsHtml}
                    </div>
                </div>

                <div class="card">
                    <div class="card-header">Managed Models</div>
                    <div class="card-body">
                        ${modelCards || '<div class="text-muted">No managed models available.</div>'}
                    </div>
                </div>
            `);

            this.$usersContent.off('click.localllm');
            this.$usersContent.on('click.localllm', '.js-local-llm-refresh', () => this.loadLocalLLM());
            this.$usersContent.on('click.localllm', '.js-local-llm-action', (event) => {
                const $button = $(event.currentTarget);
                const action = $button.data('action');
                const modelID = $button.data('model-id') || '';
                const jobID = $button.data('job-id') || '';
                const runtimeVersionValue = $button.data('runtime-version') || '';
                this.runLocalLLMAction(action, { modelID, jobID, runtimeVersion: runtimeVersionValue });
            });
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
                    <td><span class="text-muted">${user.acpAllowed ? 'yes' : 'no'}</span></td>
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
                            <thead><tr><th>Username</th><th>Name</th><th>Role</th><th>Telegram</th><th>WhatsApp</th><th>Password</th><th>ACP</th><th class="text-end">Actions</th></tr></thead>
                            <tbody>${usersRows || '<tr><td colspan="8" class="text-center text-muted py-4">No users configured</td></tr>'}</tbody>
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
                                    <div class="form-check mt-2">
                                        <input class="form-check-input js-user-form-field" type="checkbox" data-user-field="acpAllowed"${this.userForm.acpAllowed ? ' checked' : ''} id="userAcpAllowed">
                                        <label class="form-check-label" for="userAcpAllowed">Allow ACP</label>
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
                    acpAllowed: !!user.acpAllowed,
                    thinking: !!user.thinking,
                    thinking_level: user.thinking_level || '',
                    sandbox: user.sandbox !== false
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
                    acpAllowed: false,
                    thinking: false,
                    thinking_level: '',
                    sandbox: true
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
                    acpAllowed: !!this.userForm.acpAllowed,
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

        async loadA2APeers() {
            this.a2aPeersLoading = true;
            this.a2aPeersError = '';
            this.a2aPeersSuccess = '';
            this.renderA2APeersSection();
            try {
                const [configResp, runtimeResp, pairingResp] = await Promise.all([
                    fetch('/setup/api/a2a/peers/config', { cache: 'no-store' }),
                    fetch('/setup/api/a2a/peers/runtime', { cache: 'no-store' }),
                    fetch('/setup/api/a2a/peers/pairing', { cache: 'no-store' })
                ]);
                const [configData, runtimeData, pairingData] = await Promise.all([
                    configResp.json(),
                    runtimeResp.json(),
                    pairingResp.json()
                ]);
                if (!configData.success) throw new Error(configData.message || 'Failed to load A2A peers');
                if (!runtimeData.success) throw new Error(runtimeData.message || 'Failed to load A2A runtime');
                if (!pairingData.success) throw new Error(pairingData.message || 'Failed to load A2A pairing');

                this.a2aPeers = Array.isArray(configData.data && configData.data.peers) ? configData.data.peers : [];
                this.a2aPeerUsers = Array.isArray(configData.data && configData.data.users) ? configData.data.users : [];
                this.a2aRuntimeStatus = (runtimeData.data && runtimeData.data.status) || {};
                this.a2aRuntimePeers = Array.isArray(runtimeData.data && runtimeData.data.peers) ? runtimeData.data.peers : [];
                this.a2aPairing = pairingData.data || {};
            } catch (err) {
                this.a2aPeersError = err.message || 'Failed to load A2A peers';
            } finally {
                this.a2aPeersLoading = false;
                this.renderA2APeersSection();
            }
        }

        renderA2APeersSection() {
            const runtimeByPeerID = {};
            this.a2aRuntimePeers.forEach(peer => {
                if (peer && peer.peerId) runtimeByPeerID[peer.peerId] = peer;
            });

            const status = this.a2aRuntimeStatus || {};
            const peerRows = this.a2aPeers.map(peer => {
                const runtime = runtimeByPeerID[peer.peerId] || {};
                const state = runtime.state || (peer.enabled ? 'trusted-configured' : 'disabled');
                const addrCount = Array.isArray(peer.addrs) ? peer.addrs.length : 0;
                return `<tr>
                    <td><span class="badge text-bg-secondary">${escapeHtml(peer.type || 'libp2p')}</span></td>
                    <td>${escapeHtml(peer.alias || '-')}</td>
                    <td><code>${escapeHtml(peer.peerId || '')}</code></td>
                    <td>${escapeHtml(peer.localUser || '-')}</td>
                    <td><span class="badge ${this.a2aStateBadgeClass(state)}">${escapeHtml(state)}</span></td>
                    <td>${peer.enabled ? '<span class="badge text-bg-success">enabled</span>' : '<span class="badge text-bg-secondary">disabled</span>'}</td>
                    <td>${addrCount}</td>
                    <td class="text-end">
                        <button class="btn btn-sm btn-outline-secondary js-a2a-peer-ping" data-peer-id="${escapeHtml(peer.peerId)}" title="Ping"><i class="bi bi-broadcast"></i></button>
                        <button class="btn btn-sm btn-outline-primary js-a2a-peer-edit" data-peer-id="${escapeHtml(peer.peerId)}" title="Edit"><i class="bi bi-pencil"></i></button>
                        <button class="btn btn-sm btn-outline-danger js-a2a-peer-delete-open" data-peer-id="${escapeHtml(peer.peerId)}" title="Delete"><i class="bi bi-trash"></i></button>
                    </td>
                </tr>`;
            }).join('');

            const userOptions = ['<option value="">(none)</option>']
                .concat(this.a2aPeerUsers.map(username => `<option value="${escapeHtml(username)}"${this.a2aPeerForm.localUser === username ? ' selected' : ''}>${escapeHtml(username)}</option>`))
                .join('');
            const runtimeSummary = `
                <div class="row g-3 mb-4">
                    <div class="col-12 col-md-6 col-xl-3"><div class="card h-100"><div class="card-body"><div class="text-muted small">Lifecycle</div><div class="fw-semibold">${escapeHtml(status.lifecycleState || '-')}</div></div></div></div>
                    <div class="col-12 col-md-6 col-xl-3"><div class="card h-100"><div class="card-body"><div class="text-muted small">Ready</div><div class="fw-semibold">${status.ready ? 'yes' : 'no'}</div></div></div></div>
                    <div class="col-12 col-md-6 col-xl-3"><div class="card h-100"><div class="card-body"><div class="text-muted small">Known peers</div><div class="fw-semibold">${escapeHtml(String(status.knownPeers || 0))}</div></div></div></div>
                    <div class="col-12 col-md-6 col-xl-3"><div class="card h-100"><div class="card-body"><div class="text-muted small">Connected peers</div><div class="fw-semibold">${escapeHtml(String(status.connectedPeers || 0))}</div></div></div></div>
                </div>`;
            const pairingAddrs = Array.isArray(this.a2aPairing.addrs) && this.a2aPairing.addrs.length
                ? this.a2aPairing.addrs.map(addr => `<code class="d-block mb-1">${escapeHtml(addr)}</code>`).join('')
                : '<span class="text-muted">Runtime not ready yet.</span>';
            const pingAlert = this.a2aPingResult
                ? `<div class="alert ${this.a2aPingResult.success ? 'alert-success' : 'alert-danger'} mb-0">Ping <code>${escapeHtml(this.a2aPingResult.peerId || this.a2aPingTarget || '')}</code>: ${escapeHtml(this.a2aPingResult.message || (this.a2aPingResult.success ? 'success' : 'failed'))}</div>`
                : '<div class="text-muted small">Use Ping on a configured peer to test live connectivity.</div>';

            this.$usersContent.html(`
                ${runtimeSummary}
                <div class="row g-4">
                    <div class="col-12 col-xl-8">
                        <div class="card mb-4">
                            <div class="card-header d-flex justify-content-between align-items-center">
                                <span>Trusted A2A Peers</span>
                                <div class="d-flex gap-2">
                                    <button class="btn btn-sm btn-outline-secondary js-a2a-peers-refresh"><i class="bi bi-arrow-clockwise"></i> Refresh</button>
                                    <button class="btn btn-sm btn-primary js-a2a-peer-add"><i class="bi bi-plus-lg"></i> Add Peer</button>
                                </div>
                            </div>
                            <div class="card-body p-0">
                                ${this.a2aPeersLoading ? '<div class="text-center py-4"><div class="spinner-border spinner-border-sm"></div></div>' : `
                                <table class="table table-hover mb-0">
                                    <thead><tr><th>Type</th><th>Alias</th><th>Peer ID</th><th>Local User</th><th>Runtime State</th><th>Trust</th><th>Addrs</th><th class="text-end">Actions</th></tr></thead>
                                    <tbody>${peerRows || '<tr><td colspan="8" class="text-center text-muted py-4">No A2A peers configured</td></tr>'}</tbody>
                                </table>`}
                            </div>
                        </div>
                    </div>
                    <div class="col-12 col-xl-4">
                        <div class="card mb-4">
                            <div class="card-header">Local Pairing Payload</div>
                            <div class="card-body">
                                <div class="mb-2"><span class="text-muted small d-block">Peer ID</span><code>${escapeHtml(this.a2aPairing.peerId || '-')}</code></div>
                                <div><span class="text-muted small d-block mb-2">Advertised Addresses</span>${pairingAddrs}</div>
                            </div>
                        </div>
                        <div class="card">
                            <div class="card-header">Ping Result</div>
                            <div class="card-body">${pingAlert}</div>
                        </div>
                    </div>
                </div>
                <div class="alert alert-danger alert-dismissible ${this.a2aPeersError ? '' : 'd-none'}" id="a2a-peers-error-alert"><span class="js-alert-text">${escapeHtml(this.a2aPeersError)}</span><button type="button" class="btn-close" aria-label="Close"></button></div>
                <div class="alert alert-success alert-dismissible ${this.a2aPeersSuccess ? '' : 'd-none'}" id="a2a-peers-success-alert"><span class="js-alert-text">${escapeHtml(this.a2aPeersSuccess)}</span><button type="button" class="btn-close" aria-label="Close"></button></div>

                <div class="modal fade" id="a2aPeerModal" tabindex="-1">
                    <div class="modal-dialog modal-lg">
                        <div class="modal-content">
                            <div class="modal-header">
                                <h5 class="modal-title">${this.a2aPeerEditing ? 'Edit A2A Peer' : 'Add A2A Peer'}</h5>
                                <button type="button" class="btn-close" data-bs-dismiss="modal"></button>
                            </div>
                            <div class="modal-body">
                                <div class="row g-3">
                                    <div class="col-12 col-md-4">
                                        <label class="form-label">Type</label>
                                        <select class="form-select js-a2a-peer-form-field${this.a2aPeerFormErrors.type ? ' is-invalid' : ''}" data-peer-field="type" ${this.a2aPeerEditing ? 'disabled' : ''}>
                                            <option value="libp2p"${(this.a2aPeerForm.type || 'libp2p') === 'libp2p' ? ' selected' : ''}>libp2p</option>
                                        </select>
                                        <div class="invalid-feedback">${escapeHtml(this.a2aPeerFormErrors.type || '')}</div>
                                    </div>
                                    <div class="col-12 col-md-8">
                                        <label class="form-label">Alias</label>
                                        <input type="text" class="form-control js-a2a-peer-form-field" data-peer-field="alias" value="${escapeHtml(this.a2aPeerForm.alias || '')}">
                                    </div>
                                    <div class="col-12">
                                        <label class="form-label">Peer ID</label>
                                        <input type="text" class="form-control js-a2a-peer-form-field${this.a2aPeerFormErrors.peerId ? ' is-invalid' : ''}" data-peer-field="peerId" value="${escapeHtml(this.a2aPeerForm.peerId || '')}" ${this.a2aPeerEditing ? 'disabled' : ''}>
                                        <div class="invalid-feedback">${escapeHtml(this.a2aPeerFormErrors.peerId || '')}</div>
                                    </div>
                                    <div class="col-12">
                                        <label class="form-label">Multiaddrs</label>
                                        <textarea class="form-control js-a2a-peer-form-field${this.a2aPeerFormErrors.addrs ? ' is-invalid' : ''}" data-peer-field="addrsText" rows="4" placeholder="/dns4/example.org/tcp/4001/p2p/...">${escapeHtml(this.a2aPeerForm.addrsText || '')}</textarea>
                                        <div class="invalid-feedback">${escapeHtml(this.a2aPeerFormErrors.addrs || '')}</div>
                                        <div class="form-text">One multiaddr per line.</div>
                                    </div>
                                    <div class="col-12 col-md-6">
                                        <label class="form-label">Local User</label>
                                        <select class="form-select js-a2a-peer-form-field${this.a2aPeerFormErrors.localUser ? ' is-invalid' : ''}" data-peer-field="localUser">${userOptions}</select>
                                        <div class="invalid-feedback">${escapeHtml(this.a2aPeerFormErrors.localUser || '')}</div>
                                    </div>
                                    <div class="col-12 col-md-6 d-flex align-items-end">
                                        <div class="form-check mb-2">
                                            <input class="form-check-input js-a2a-peer-form-field" type="checkbox" data-peer-field="enabled" id="a2aPeerEnabled"${this.a2aPeerForm.enabled ? ' checked' : ''}>
                                            <label class="form-check-label" for="a2aPeerEnabled">Enabled</label>
                                        </div>
                                    </div>
                                    <div class="col-12">
                                        <label class="form-label">Notes</label>
                                        <textarea class="form-control js-a2a-peer-form-field" data-peer-field="notes" rows="3">${escapeHtml(this.a2aPeerForm.notes || '')}</textarea>
                                    </div>
                                </div>
                            </div>
                            <div class="modal-footer">
                                <button type="button" class="btn btn-secondary" data-bs-dismiss="modal">Cancel</button>
                                <button type="button" class="btn btn-primary js-a2a-peer-save">${this.a2aPeerEditing ? 'Update' : 'Create'}</button>
                            </div>
                        </div>
                    </div>
                </div>

                <div class="modal fade" id="a2aPeerDeleteModal" tabindex="-1">
                    <div class="modal-dialog">
                        <div class="modal-content">
                            <div class="modal-header">
                                <h5 class="modal-title">Delete A2A Peer</h5>
                                <button type="button" class="btn-close" data-bs-dismiss="modal"></button>
                            </div>
                            <div class="modal-body">
                                <p>Delete peer <code>${escapeHtml(this.a2aPeerDeleteID || '')}</code>?</p>
                                <p class="text-danger small mb-0">This removes the local trust record immediately.</p>
                            </div>
                            <div class="modal-footer">
                                <button type="button" class="btn btn-secondary" data-bs-dismiss="modal">Cancel</button>
                                <button type="button" class="btn btn-danger js-a2a-peer-delete-confirm"${this.a2aPeerDeleting ? ' disabled' : ''}>Delete</button>
                            </div>
                        </div>
                    </div>
                </div>
            `);

            this.$usersContent.find('#a2a-peers-error-alert .btn-close').on('click', () => {
                this.a2aPeersError = '';
                this.renderA2APeersSection();
            });
            this.$usersContent.find('#a2a-peers-success-alert .btn-close').on('click', () => {
                this.a2aPeersSuccess = '';
                this.renderA2APeersSection();
            });

            this.$usersContent.off('click.a2aPeers');
            this.$usersContent.off('input.a2aPeers change.a2aPeers');
            this.$usersContent.on('click.a2aPeers', '.js-a2a-peers-refresh', () => this.loadA2APeers());
            this.$usersContent.on('click.a2aPeers', '.js-a2a-peer-add', () => this.openA2APeerModal(false));
            this.$usersContent.on('click.a2aPeers', '.js-a2a-peer-edit', (event) => this.openA2APeerModal(true, $(event.currentTarget).data('peer-id')));
            this.$usersContent.on('click.a2aPeers', '.js-a2a-peer-delete-open', (event) => this.openDeleteA2APeerModal($(event.currentTarget).data('peer-id')));
            this.$usersContent.on('click.a2aPeers', '.js-a2a-peer-save', () => this.saveA2APeer());
            this.$usersContent.on('click.a2aPeers', '.js-a2a-peer-delete-confirm', () => this.deleteA2APeer());
            this.$usersContent.on('click.a2aPeers', '.js-a2a-peer-ping', (event) => this.pingA2APeer($(event.currentTarget).data('peer-id')));
            this.$usersContent.on('input.a2aPeers change.a2aPeers', '.js-a2a-peer-form-field', (event) => this.handleA2APeerFormInput(event));
        }

        a2aStateBadgeClass(state) {
            switch (state) {
                case 'connected-authorized':
                    return 'text-bg-success';
                case 'connected-relayed':
                    return 'text-bg-info';
                case 'trusted-configured':
                    return 'text-bg-primary';
                case 'discovered-untrusted':
                    return 'text-bg-warning';
                case 'disconnected':
                    return 'text-bg-secondary';
                default:
                    return 'text-bg-light';
            }
        }

        openA2APeerModal(editing, peerID) {
            this.a2aPeerEditing = editing;
            this.a2aPeerFormErrors = {};
            if (editing) {
                const peer = this.a2aPeers.find(item => item.peerId === peerID);
                if (!peer) return;
                this.a2aPeerForm = {
                    type: peer.type || 'libp2p',
                    alias: peer.alias || '',
                    peerId: peer.peerId || '',
                    addrsText: Array.isArray(peer.addrs) ? peer.addrs.join('\n') : '',
                    localUser: peer.localUser || '',
                    enabled: !!peer.enabled,
                    notes: peer.notes || ''
                };
            } else {
                this.a2aPeerForm = {
                    type: 'libp2p',
                    alias: '',
                    peerId: '',
                    addrsText: '',
                    localUser: '',
                    enabled: true,
                    notes: ''
                };
            }
            this.renderA2APeersSection();
            this.a2aPeerModal = new bootstrap.Modal(document.getElementById('a2aPeerModal'));
            this.a2aPeerModal.show();
        }

        openDeleteA2APeerModal(peerID) {
            this.a2aPeerDeleteID = peerID;
            this.renderA2APeersSection();
            this.a2aPeerDeleteModal = new bootstrap.Modal(document.getElementById('a2aPeerDeleteModal'));
            this.a2aPeerDeleteModal.show();
        }

        handleA2APeerFormInput(event) {
            const $input = $(event.currentTarget);
            const field = $input.data('peer-field');
            if (!field) return;
            this.a2aPeerForm[field] = $input.is(':checkbox') ? $input.is(':checked') : $input.val();
            if (field === 'addrsText') {
                delete this.a2aPeerFormErrors.addrs;
            } else {
                delete this.a2aPeerFormErrors[field];
            }
            $input.removeClass('is-invalid');
            const $feedback = $input.siblings('.invalid-feedback');
            if ($feedback.length) {
                $feedback.text('');
            }
        }

        async saveA2APeer() {
            this.a2aPeerFormErrors = {};
            this.a2aPeersError = '';
            this.a2aPeersSuccess = '';
            try {
                const peerID = this.a2aPeerForm.peerId || '';
                const payload = {
                    type: this.a2aPeerForm.type || 'libp2p',
                    alias: this.a2aPeerForm.alias || '',
                    peerId: peerID,
                    addrs: parseLineList(this.a2aPeerForm.addrsText),
                    localUser: this.a2aPeerForm.localUser || '',
                    enabled: !!this.a2aPeerForm.enabled,
                    notes: this.a2aPeerForm.notes || ''
                };
                const url = this.a2aPeerEditing ? `/setup/api/a2a/peers/${encodeURIComponent(peerID)}` : '/setup/api/a2a/peers';
                const method = this.a2aPeerEditing ? 'PUT' : 'POST';
                const resp = await fetch(url, {
                    method,
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(payload)
                });
                const data = await resp.json();
                if (!data.success) {
                    if (data.errors) {
                        this.a2aPeerFormErrors = data.errors;
                        this.renderA2APeersSection();
                        if (this.a2aPeerModal) this.a2aPeerModal.show();
                        return;
                    }
                    throw new Error(data.message || 'Failed to save A2A peer');
                }
                if (this.a2aPeerModal) this.a2aPeerModal.hide();
                this.a2aPeersSuccess = this.a2aPeerEditing ? 'Peer updated' : 'Peer created';
                await this.loadA2APeers();
            } catch (err) {
                this.a2aPeersError = err.message || 'Failed to save A2A peer';
                this.renderA2APeersSection();
            }
        }

        async deleteA2APeer() {
            this.a2aPeerDeleting = true;
            try {
                const resp = await fetch(`/setup/api/a2a/peers/${encodeURIComponent(this.a2aPeerDeleteID)}`, { method: 'DELETE' });
                const data = await resp.json();
                if (!data.success) throw new Error(data.message || 'Failed to delete A2A peer');
                if (this.a2aPeerDeleteModal) this.a2aPeerDeleteModal.hide();
                this.a2aPeersSuccess = 'Peer deleted';
                await this.loadA2APeers();
            } catch (err) {
                this.a2aPeersError = err.message || 'Failed to delete A2A peer';
                this.renderA2APeersSection();
            } finally {
                this.a2aPeerDeleting = false;
            }
        }

        async pingA2APeer(peerID) {
            this.a2aPeersError = '';
            this.a2aPingTarget = peerID || '';
            try {
                const resp = await fetch('/setup/api/a2a/ping', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ peerId: peerID })
                });
                const data = await resp.json();
                if (!data.success) throw new Error(data.message || 'Ping failed');
                this.a2aPingResult = data.data || { success: true, message: data.message || 'success', peerId };
                this.renderA2APeersSection();
            } catch (err) {
                this.a2aPingResult = { success: false, message: err.message || 'Ping failed', peerId };
                this.renderA2APeersSection();
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
            this.$statusNote = $('#wizard-step-status-note');
            this.$statusNoteBody = $('#wizard-step-status-note-body');
            this.$statusNoteIcon = $('#wizard-step-status-note-icon');
            this.$statusNoteText = $('#wizard-step-status-note-text');
            this.completeModal = new bootstrap.Modal(document.getElementById('completeModal'));
            this.restartModal = new bootstrap.Modal(document.getElementById('wizardRestartModal'));
            this.appliedModal = new bootstrap.Modal(document.getElementById('wizardAppliedModal'));
            this.$restartMessage = $('#wizard-restart-message');
            this.$restartDetail = $('#wizard-restart-detail');

            this.steps = [];
            this.step = 1;
            this.totalSteps = 1;
            this.wizardData = {};
            this.currentStep = {};
            this.fieldErrors = {};
            this.loading = false;
            this.saving = false;
            this.finishSaved = false;
            this.lastKnownInstanceID = null;
            this.pairingStatus = {};
            this.pairingPollers = {};
        }

        init() {
            this.ensureWizardInteractionStyles();
            this.bindEvents();
            this.loadState();
        }

        bindEvents() {
            this.$errorAlert.find('.btn-close').on('click', () => hideAlert(this.$errorAlert));
            this.$prev.on('click', () => this.prevStep());
            this.$next.on('click', () => {
                console.debug('[setup wizard] next clicked', {
                    step: this.step,
                    totalSteps: this.totalSteps,
                    stepID: this.currentStep && this.currentStep.id ? this.currentStep.id : '',
                    loading: this.loading,
                    saving: this.saving
                });
                this.nextStep();
            });
            this.$stepContent.on('input change', '.js-bound-field', (event) => this.handleFieldChange(event));
            this.$stepContent.on('click', '.js-pairing-start', (event) => this.startPairing(event));
            this.$stepContent.on('click', '.js-pairing-cancel', (event) => this.cancelPairing(event));
            this.$stepContent.on('click', '.js-pairing-refresh', (event) => this.refreshPairing(event));
            $('#wizard-close-btn').on('click', () => this.closeWizard());
            $('#wizard-apply-btn').on('click', () => this.applyAfterFinish());
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
                this.stopAllPairingPollers();
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
            this.enhanceAgentEmojiField();
            this.renderFieldErrors(this.$stepContent, this.fieldErrors);
            this.initializePairingStep();
            this.renderReview();
            this.syncNav();
        }

        populateFields($container, state) {
            $container.find('.js-bound-field').each((_, el) => {
                const $field = $(el);
                const bindPath = $field.data('bind');
                const bindType = $field.data('bind-type') || 'string';
                const scale = this.readNumericScale($field);
                const value = getByPath(state, bindPath);
                if ($field.is(':checkbox')) {
                    $field.prop('checked', !!value);
                } else if (bindType === 'string-list') {
                    $field.val(formatStringList(value));
                } else if (bindType === 'number') {
                    $field.val(value == null ? '' : value / scale);
                    this.updateWizardSliderDisplay($field);
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
            const scale = this.readNumericScale($field);
            let value;
            if ($field.is(':checkbox')) {
                value = $field.is(':checked');
            } else if (bindType === 'number') {
                const raw = $field.val();
                value = raw === '' ? 0 : Math.round(Number(raw) * scale);
                this.updateWizardSliderDisplay($field);
            } else if (bindType === 'string-list') {
                value = parseStringList($field.val());
            } else {
                value = $field.val();
            }
            setByPath(this.wizardData, bindPath, value);

            if (this.currentStep.id === 'security') {
                if (bindPath === 'SandboxPreset') {
                    this.applySecurityPresetDefaults(value);
                } else if (bindPath === 'SandboxAdvanced' && !value) {
                    if ((this.wizardData.SandboxPreset || '').toLowerCase() !== 'custom') {
                        this.applySecurityPresetDefaults(this.wizardData.SandboxPreset);
                    }
                }
            }

            this.applyShowWhen(this.$stepContent, this.wizardData);
            this.populateFields(this.$stepContent, this.wizardData);
            this.syncNav();

            if (this.currentStep.id === 'llm' && bindPath === 'LLMProviderID' && value && value !== 'custom') {
                this.refreshModelOptions(value);
            }
        }

        updateWizardSliderDisplay($field) {
            if (!$field.hasClass('js-slider-field')) return;
            const fieldID = $field.attr('id');
            const unit = $field.data('unit') || '';
            const rawValue = String($field.val() ?? '');
            const formatted = rawValue.replace(/\.0$/, '');
            this.$stepContent.find(`.js-slider-value[data-slider-for="${fieldID}"]`).text(unit ? `${formatted} ${unit}` : formatted);
        }

        readNumericScale($field) {
            const raw = Number($field.data('scale'));
            return Number.isFinite(raw) && raw > 0 ? raw : 1;
        }

        initializePairingStep() {
            if (!this.currentStep || this.currentStep.id !== 'pairing') {
                return;
            }
            this.refreshAllPairings();
        }

        stopAllPairingPollers() {
            Object.values(this.pairingPollers).forEach((timer) => {
                if (timer) window.clearTimeout(timer);
            });
            this.pairingPollers = {};
        }

        async refreshAllPairings() {
            const channels = [];
            if (this.wizardData.TelegramEnabled) channels.push('telegram');
            if (this.wizardData.WhatsAppEnabled) channels.push('whatsapp');
            await Promise.all(channels.map((channel) => this.fetchPairingStatus(channel)));
            this.syncNav();
        }

        async refreshPairing(event) {
            const channel = $(event.currentTarget).closest('[data-pairing-channel]').data('pairing-channel');
            if (!channel) return;
            await this.fetchPairingStatus(channel);
            this.syncNav();
        }

        async startPairing(event) {
            const channel = $(event.currentTarget).closest('[data-pairing-channel]').data('pairing-channel');
            if (!channel) return;
            try {
                const resp = await fetch(`/setup/api/wizard/pairing/${encodeURIComponent(channel)}/start`, { method: 'POST' });
                const data = await resp.json();
                if (!data.success) throw new Error(data.message || `Failed to start ${channel} pairing`);
                this.applyPairingStatus(channel, data.data || {});
                this.schedulePairingPoll(channel, data.data || {});
                this.syncNav();
            } catch (err) {
                showAlert(this.$errorAlert, err.message || `Failed to start ${channel} pairing`);
            }
        }

        async cancelPairing(event) {
            const channel = $(event.currentTarget).closest('[data-pairing-channel]').data('pairing-channel');
            if (!channel) return;
            try {
                const resp = await fetch(`/setup/api/wizard/pairing/${encodeURIComponent(channel)}/cancel`, { method: 'POST' });
                const data = await resp.json();
                if (!data.success) throw new Error(data.message || `Failed to cancel ${channel} pairing`);
                this.applyPairingStatus(channel, data.data || {});
                this.schedulePairingPoll(channel, data.data || {});
                this.syncNav();
            } catch (err) {
                showAlert(this.$errorAlert, err.message || `Failed to cancel ${channel} pairing`);
            }
        }

        async fetchPairingStatus(channel) {
            try {
                const resp = await fetch(`/setup/api/wizard/pairing/${encodeURIComponent(channel)}/status`);
                const data = await resp.json();
                if (!data.success) throw new Error(data.message || `Failed to load ${channel} pairing status`);
                this.applyPairingStatus(channel, data.data || {});
                this.schedulePairingPoll(channel, data.data || {});
            } catch (err) {
                this.renderPairingError(channel, err.message || `Failed to load ${channel} pairing status`);
            }
        }

        schedulePairingPoll(channel, status) {
            if (this.pairingPollers[channel]) {
                window.clearTimeout(this.pairingPollers[channel]);
                delete this.pairingPollers[channel];
            }
            const state = status && status.state ? status.state : '';
            if (['paired', 'expired', 'failed', 'cancelled', 'not_started'].includes(state)) {
                return;
            }
            const delay = Number(status && status.pollAfterMs) > 0 ? Number(status.pollAfterMs) : 1500;
            this.pairingPollers[channel] = window.setTimeout(() => this.fetchPairingStatus(channel), delay);
        }

        applyPairingStatus(channel, status) {
            this.pairingStatus[channel] = status || {};
            if (channel === 'telegram' && status && status.identity && status.identity.id) {
                this.wizardData.UserTelegramID = status.identity.id;
            }
            if (channel === 'whatsapp' && status && status.identity && status.identity.id) {
                this.wizardData.UserWhatsAppID = status.identity.id;
            }
            this.renderPairingStatus(channel, status || {});
        }

        renderPairingError(channel, message) {
            const $card = this.$stepContent.find(`[data-pairing-channel="${channel}"]`);
            if (!$card.length) return;
            $card.find('.js-pairing-badge').attr('class', 'badge text-bg-danger js-pairing-badge').text('Error');
            $card.find('.js-pairing-message').text(message || 'Pairing failed.');
        }

        renderPairingStatus(channel, status) {
            const $card = this.$stepContent.find(`[data-pairing-channel="${channel}"]`);
            if (!$card.length) return;

            const state = String(status.state || 'not_started');
            const badge = $card.find('.js-pairing-badge');
            const message = $card.find('.js-pairing-message');
            const artifact = $card.find('.js-pairing-artifact');
            const identity = $card.find('.js-pairing-identity');
            const startBtn = $card.find('.js-pairing-start');
            const cancelBtn = $card.find('.js-pairing-cancel');

            const badgeMap = {
                not_started: ['text-bg-secondary', 'Not started'],
                waiting: ['text-bg-warning', 'Waiting'],
                paired: ['text-bg-success', 'Paired'],
                expired: ['text-bg-danger', 'Expired'],
                failed: ['text-bg-danger', 'Failed'],
                cancelled: ['text-bg-secondary', 'Cancelled']
            };
            const badgeInfo = badgeMap[state] || badgeMap.not_started;
            badge.attr('class', `badge ${badgeInfo[0]} js-pairing-badge`).text(badgeInfo[1]);
            message.text(status.message || `${channel} pairing has not started yet.`);

            startBtn.text(state === 'paired' ? 'Restart Pairing' : 'Start Pairing');
            cancelBtn.toggleClass('d-none', state !== 'waiting');

            this.renderPairingArtifact(channel, artifact, status);
            this.renderPairingIdentity(identity, channel, status);
        }

        renderPairingArtifact(channel, $artifact, status) {
            const artifacts = status && status.artifacts ? status.artifacts : {};
            if (channel === 'telegram' && artifacts.code) {
                $artifact.removeClass('d-none').html(`
                    <div class="fw-semibold mb-1">One-time code</div>
                    <code class="fs-5">${escapeHtml(artifacts.code)}</code>
                    <div class="small text-muted mt-1">Send this exact code to the Telegram bot from the owner account.</div>
                `);
                return;
            }
            if (channel === 'whatsapp' && artifacts.qrCode) {
                $artifact.removeClass('d-none').html(`
                    <div class="fw-semibold mb-2">Scan this QR code</div>
                    <div class="bg-white rounded p-3 d-inline-block js-whatsapp-qr"></div>
                    <div class="small text-muted mt-2">${escapeHtml(artifacts.qrLabel || '')}</div>
                `);
                const container = $artifact.find('.js-whatsapp-qr').get(0);
                if (container && typeof window.QRCode !== 'undefined') {
                    container.innerHTML = '';
                    // eslint-disable-next-line no-new
                    new window.QRCode(container, {
                        text: artifacts.qrCode,
                        width: 220,
                        height: 220
                    });
                }
                return;
            }
            $artifact.addClass('d-none').empty();
        }

        renderPairingIdentity($identity, channel, status) {
            const identity = status && status.identity ? status.identity : null;
            const staged = channel === 'telegram' ? this.wizardData.UserTelegramID : this.wizardData.UserWhatsAppID;
            if (identity) {
                if (channel === 'telegram') {
                    const parts = [identity.displayName, identity.username ? `@${identity.username}` : '', identity.id].filter(Boolean);
                    $identity.text(`Paired owner: ${parts.join(' · ')}`);
                } else {
                    const parts = [identity.phone, identity.jid || identity.id].filter(Boolean);
                    $identity.text(`Paired owner: ${parts.join(' · ')}`);
                }
                return;
            }
            if (staged) {
                $identity.text(`Currently staged owner ID: ${staged}`);
                return;
            }
            $identity.text('');
        }

        enhanceAgentEmojiField() {
            if (!this.currentStep || this.currentStep.id !== 'agent') return;
            const $input = this.$stepContent.find('.js-bound-field[data-bind="AgentEmoji"]').first();
            if (!$input.length || $input.data('emoji-enhanced')) return;
            $input.data('emoji-enhanced', true);

            const $group = $('<div class="input-group"></div>');
            $input.wrap($group);

            const $button = $('<button type="button" class="btn btn-outline-secondary dropdown-toggle" data-bs-toggle="dropdown" data-bs-auto-close="false" aria-expanded="false" title="Pick emoji">😀</button>');
            const $menu = $('<div class="dropdown-menu p-0 shadow emoji-picker-dropdown"></div>');

            const picker = document.createElement('emoji-picker');
            picker.style.width = '320px';
            picker.style.height = '360px';
            picker.addEventListener('emoji-click', (event) => {
                const emoji = event && event.detail ? event.detail.unicode : '';
                if (!emoji) return;
                $input.val(emoji);
                $input.trigger('input');
                const dropdown = bootstrap.Dropdown.getOrCreateInstance($button.get(0));
                dropdown.hide();
            });

            const $quick = $('<div class="emoji-priority-row emoji-priority-row-bottom"></div>');
            const quickPicks = ['🤖', '🦞', '⚙️', '🦾', '🔧', '🛠️', '🧠', '🛰️', '🖥️', '📡', '🐾', '🪛'];
            const $quickButtons = $('<div class="emoji-priority-buttons"></div>');
            quickPicks.forEach((emoji) => {
                const $emojiBtn = $(`<button type="button" class="emoji-priority-btn" title="${emoji}">${emoji}</button>`);
                if (emoji === '🤖') {
                    $emojiBtn.addClass('emoji-priority-btn-primary');
                }
                $emojiBtn.on('click', () => {
                    $input.val(emoji);
                    $input.trigger('input');
                    const dropdown = bootstrap.Dropdown.getOrCreateInstance($button.get(0));
                    dropdown.hide();
                });
                $quickButtons.append($emojiBtn);
            });
            $quick.append($quickButtons);

            $menu.append(picker, $quick);
            $input.after($button);
            $button.after($menu);

            if (!document.getElementById('emoji-priority-style')) {
                const style = document.createElement('style');
                style.id = 'emoji-priority-style';
                style.textContent = `
                    .emoji-picker-dropdown {
                        margin-top: 6px !important;
                        min-width: 320px;
                        z-index: 2000;
                    }
                    .emoji-priority-row {
                        background: #1e1f26;
                        border-top: 1px solid #4a4d59;
                        padding: 6px 8px 4px;
                        width: 320px;
                        box-sizing: border-box;
                    }
                    .emoji-priority-buttons {
                        display: flex;
                        flex-wrap: wrap;
                        gap: 2px 6px;
                    }
                    .emoji-priority-btn {
                        border: 0;
                        background: transparent;
                        color: #fff;
                        font-size: 18px;
                        line-height: 1;
                        border-radius: 6px;
                        padding: 2px 4px;
                        width: 20px;
                        height: 24px;
                        display: inline-flex;
                        align-items: center;
                        justify-content: center;
                    }
                    .emoji-priority-btn:hover,
                    .emoji-priority-btn:focus {
                        background: rgba(255,255,255,0.14);
                        outline: none;
                    }
                    .emoji-priority-btn-primary {
                        background: rgba(32,118,255,0.35);
                    }
                `;
                document.head.appendChild(style);
            }
        }

        applySecurityPresetDefaults(preset) {
            const normalized = (preset || 'assistant').toLowerCase();
            this.wizardData.SandboxConsentPermissive = false;
            this.wizardData.SandboxConsentAssistant = false;
            this.wizardData.SandboxConsentHardened = false;

            if (normalized === 'permissive') {
                this.wizardData.SandboxEnabled = false;
                this.wizardData.ExecSandboxEnabled = false;
                this.wizardData.BrowserSandboxEnabled = false;
                this.wizardData.FileToolsSandboxEnabled = false;
                this.wizardData.SandboxMode = 'home';
                return;
            }

            if (normalized === 'hardened') {
                this.wizardData.SandboxEnabled = true;
                this.wizardData.ExecSandboxEnabled = true;
                this.wizardData.BrowserSandboxEnabled = true;
                this.wizardData.FileToolsSandboxEnabled = true;
                this.wizardData.SandboxMode = 'home';
                return;
            }

            if (normalized === 'custom') {
                this.wizardData.SandboxAdvanced = true;
                return;
            }

            this.wizardData.SandboxEnabled = true;
            this.wizardData.ExecSandboxEnabled = true;
            this.wizardData.BrowserSandboxEnabled = true;
            this.wizardData.FileToolsSandboxEnabled = true;
            this.wizardData.SandboxMode = 'autodocs-write';
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
                        <tr><th>Agent</th><td>${escapeHtml(((this.wizardData.AgentEmoji || '').trim() ? `${this.wizardData.AgentEmoji} ` : '') + (this.wizardData.AgentName || 'GoClaw'))}</td></tr>
                        <tr><th>Owner</th><td>${escapeHtml((this.wizardData.UserDisplayName || '') + ' (' + (this.wizardData.UserName || '') + ')')}</td></tr>
                        <tr><th>HTTP Server</th><td>${escapeHtml(this.wizardData.HTTPEnabled ? this.wizardData.HTTPListen : 'Disabled')}</td></tr>
                        <tr><th>Telegram</th><td>${escapeHtml(this.wizardData.TelegramEnabled ? 'Enabled' : 'Disabled')}</td></tr>
                        <tr><th>WhatsApp</th><td>${escapeHtml(this.wizardData.WhatsAppEnabled ? 'Enabled' : 'Disabled')}</td></tr>
                        <tr><th>Security</th><td>${escapeHtml(this.reviewSecuritySummary())}</td></tr>
                        <tr><th>LLM</th><td>${escapeHtml(this.reviewLLMSummary())}</td></tr>
                        <tr><th>Voice LLM</th><td>${escapeHtml(this.reviewVoiceSummary())}</td></tr>
                        <tr><th>Speech-to-Text</th><td>${escapeHtml(this.reviewSTTSummary())}</td></tr>
                    </tbody>
                </table>
            `);
        }

        reviewSecuritySummary() {
            const preset = String(this.wizardData.SandboxPreset || 'assistant').toLowerCase();
            if (preset === 'permissive') {
                return 'Permissive - least restricted, best flexibility';
            }
            if (preset === 'hardened') {
                return 'Hardened - stronger protection with reduced capability';
            }
            if (preset === 'custom') {
                return 'Custom - advanced manually selected security settings';
            }
            return 'Assistant - balanced protection for normal use';
        }

        reviewLLMSummary() {
            if (!this.wizardData || this.wizardData.LLMSkipped || !this.wizardData.LLMProviderID) {
                return 'Not configured';
            }
            const provider = this.wizardData.LLMProviderName || this.wizardData.LLMProviderID;
            let model = this.wizardData.LLMModel || '';
            if ((this.wizardData.LLMOnboardingChoice || '') === 'local_gemma' && this.wizardData.LLMManagedModelID) {
                model = this.wizardData.LLMManagedModelID;
            }
            return model ? `${provider} - ${model}` : provider;
        }

        reviewVoiceSummary() {
            if (!this.wizardData || !this.wizardData.VoiceLLMEnabled) {
                return 'Disabled';
            }
            return this.wizardData.VoiceLLMVoice ? `Enabled (${this.wizardData.VoiceLLMVoice})` : 'Enabled';
        }

        reviewSTTSummary() {
            if (!this.wizardData || !this.wizardData.STTEnabled) {
                return 'Disabled';
            }
            return this.wizardData.STTModel ? `Enabled (${this.wizardData.STTModel})` : 'Enabled';
        }

        syncNav() {
            this.$prev.prop('disabled', this.step <= 1 || this.loading || this.saving);
            const blocker = this.getCurrentStepBlocker();
            const hardDisabled = this.loading || this.saving;
            this.$next.prop('disabled', hardDisabled);
            this.$next.toggleClass('wizard-soft-disabled', !hardDisabled && !!blocker);
            this.$next.attr('aria-disabled', !hardDisabled && blocker ? 'true' : 'false');
            this.$next.attr('title', blocker && !hardDisabled ? blocker.reason : '');
            this.$nextLabel.text(this.step === this.totalSteps ? 'Finish' : 'Next');
            this.$nextIcon.attr('class', `bi ${this.step === this.totalSteps ? 'bi-check-lg' : 'bi-arrow-right'}`);
            this.$nextSpinner.toggleClass('d-none', !this.saving);
            this.renderStepStatusNote(blocker);
        }

        renderStepStatusNote(blocker) {
            if (!this.currentStep) {
                this.$statusNote.addClass('d-none');
                this.$statusNoteBody.attr('class', 'text-success');
                this.$statusNoteIcon.attr('class', 'bi bi-check2-circle me-1');
                this.$statusNoteText.text('');
                return;
            }

            if (blocker) {
                this.$statusNote.removeClass('d-none');
                this.$statusNoteBody.attr('class', 'text-warning');
                this.$statusNoteIcon.attr('class', 'bi bi-exclamation-triangle me-1');
                this.$statusNoteText.text(blocker.reason || 'Complete the required step before continuing.');
                return;
            }

            if (this.currentStep.id === 'pairing') {
                this.$statusNote.removeClass('d-none');
                this.$statusNoteBody.attr('class', 'text-success');
                this.$statusNoteIcon.attr('class', 'bi bi-check2-circle me-1');
                this.$statusNoteText.text('All required channels are paired. You can continue.');
                return;
            }

            this.$statusNote.addClass('d-none');
            this.$statusNoteBody.attr('class', 'text-success');
            this.$statusNoteIcon.attr('class', 'bi bi-check2-circle me-1');
            this.$statusNoteText.text('');
        }

        getCurrentStepBlocker() {
            if (!this.currentStep) {
                return null;
            }
            if (this.currentStep.id === 'security') {
                return this.getConsentBlocker();
            }
            if (this.currentStep.id === 'pairing') {
                return this.getPairingBlocker();
            }
            return null;
        }

        getConsentBlocker() {
            const preset = (this.wizardData.SandboxPreset || 'assistant').toLowerCase();
            if (preset === 'custom') {
                return null;
            }

            if (preset === 'permissive' && !this.wizardData.SandboxConsentPermissive) {
                return {
                    reason: 'You must acknowledge the permissive security warning before continuing.',
                    targetPath: 'SandboxConsentPermissive'
                };
            }
            if (preset === 'hardened' && !this.wizardData.SandboxConsentHardened) {
                return {
                    reason: 'You must acknowledge the hardened security note before continuing.',
                    targetPath: 'SandboxConsentHardened'
                };
            }
            if (preset !== 'permissive' && preset !== 'hardened' && !this.wizardData.SandboxConsentAssistant) {
                return {
                    reason: 'You must acknowledge the assistant security guidance before continuing.',
                    targetPath: 'SandboxConsentAssistant'
                };
            }
            return null;
        }

        getPairingBlocker() {
            if (this.wizardData.TelegramEnabled && !this.wizardData.UserTelegramID) {
                return {
                    reason: 'Complete Telegram pairing before continuing.',
                    channel: 'telegram'
                };
            }
            if (this.wizardData.WhatsAppEnabled && !this.wizardData.UserWhatsAppID) {
                return {
                    reason: 'Complete WhatsApp pairing before continuing.',
                    channel: 'whatsapp'
                };
            }
            return null;
        }

        focusBlockedStep(blocker) {
            if (!blocker) {
                return;
            }
            console.debug('[setup wizard] focusBlockedStep', blocker);
            this.renderStepStatusNote(blocker);
            this.ensureWizardInteractionStyles();
            const $target = this.findBlockedTarget(blocker);
            const $action = this.findBlockedAction(blocker, $target);
            console.debug('[setup wizard] focusBlockedStep target', {
                count: $target.length,
                className: $target.length ? ($target.attr('class') || '') : '',
                bind: blocker.targetPath || '',
                channel: blocker.channel || ''
            });
            console.debug('[setup wizard] focusBlockedStep action', {
                count: $action.length,
                className: $action.length ? ($action.attr('class') || '') : ''
            });
            if (!$target.length) {
                console.warn('[setup wizard] blocked step target not found', blocker);
                return;
            }
            const node = $target.get(0);
            if (node && typeof node.scrollIntoView === 'function') {
                node.scrollIntoView({ behavior: 'smooth', block: 'center' });
                console.debug('[setup wizard] scrolled blocked target into view');
            }
            $target.addClass('wizard-blocked-focus');
            console.debug('[setup wizard] applied wizard-blocked-focus class');
            window.setTimeout(() => $target.removeClass('wizard-blocked-focus'), 650);
            if ($action.length) {
                $action.addClass('wizard-blocked-action-focus');
                console.debug('[setup wizard] applied wizard-blocked-action-focus class');
                window.setTimeout(() => $action.removeClass('wizard-blocked-action-focus'), 900);
            }
        }

        findBlockedTarget(blocker) {
            if (!blocker) {
                return $();
            }
            if (blocker.targetPath) {
                const $field = this.$stepContent.find(`.js-bound-field[data-bind="${blocker.targetPath}"]`).first();
                if ($field.length) {
                    return $field.closest('.js-field');
                }
            }
            if (blocker.channel) {
                return this.$stepContent.find(`[data-pairing-channel="${blocker.channel}"]`).first();
            }
            return $();
        }

        findBlockedAction(blocker, $target) {
            if (!blocker) {
                return $();
            }
            if (blocker.channel && $target && $target.length) {
                return $target.find('.js-pairing-start').first();
            }
            return $();
        }

        ensureWizardInteractionStyles() {
            if (document.getElementById('wizard-interaction-style')) {
                console.debug('[setup wizard] interaction styles already installed');
                return;
            }
            const style = document.createElement('style');
            style.id = 'wizard-interaction-style';
            style.textContent = `
                .wizard-soft-disabled {
                    opacity: 0.65;
                    filter: saturate(0.75);
                }
                .wizard-blocked-focus {
                    animation: wizard-blocked-shake 420ms ease-in-out 1, wizard-blocked-flash 700ms ease-out 1;
                    box-shadow: 0 0 0 3px rgba(255, 193, 7, 0.28);
                    border-radius: 10px;
                    background: rgba(255, 193, 7, 0.18);
                }
                .wizard-blocked-action-focus {
                    animation: wizard-blocked-action-pulse 900ms ease-out 1;
                    box-shadow: 0 0 0 4px rgba(255, 193, 7, 0.26), 0 0 18px rgba(255, 193, 7, 0.38);
                }
                @keyframes wizard-blocked-shake {
                    0%, 100% { transform: translateX(0); }
                    15% { transform: translateX(-10px); }
                    30% { transform: translateX(10px); }
                    45% { transform: translateX(-8px); }
                    60% { transform: translateX(8px); }
                    75% { transform: translateX(-4px); }
                }
                @keyframes wizard-blocked-flash {
                    0% { background: rgba(255, 193, 7, 0.30); }
                    100% { background: rgba(255, 193, 7, 0.12); }
                }
                @keyframes wizard-blocked-action-pulse {
                    0% { transform: scale(1); }
                    35% { transform: scale(1.04); }
                    100% { transform: scale(1); }
                }
            `;
            document.head.appendChild(style);
            console.debug('[setup wizard] installed interaction styles');
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
            const blocker = this.getCurrentStepBlocker();
            console.debug('[setup wizard] nextStep blocker check', blocker);
            if (blocker && !this.loading && !this.saving) {
                this.focusBlockedStep(blocker);
                return;
            }

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
                this.finishSaved = true;
                this.completeModal.show();
            } catch (err) {
                showAlert(this.$errorAlert, err.message || 'Failed to save configuration');
            } finally {
                this.saving = false;
                this.syncNav();
            }
        }

        async applyAfterFinish() {
            if (!this.finishSaved) return;
            showAlert(this.$errorAlert, '');
            try {
                this.lastKnownInstanceID = await captureCurrentInstanceID();
                const applyResp = await fetch('/setup/api/apply', { method: 'POST' });
                const applyData = await applyResp.json();
                if (!applyData.success) throw new Error(applyData.message || 'Failed to apply configuration');
                const apply = extractApplyResult(applyData);
                await this.handleApplyAfterFinish(apply, this.lastKnownInstanceID);
            } catch (err) {
                showAlert(this.$errorAlert, err.message || 'Failed to apply configuration');
            }
        }

        async handleApplyAfterFinish(apply, previousInstanceID) {
            if (apply.action === 'manual_restart') {
                this.completeModal.hide();
                this.$restartMessage.text(apply.message || 'Stop and restart the gateway process to apply changes.');
                this.$restartDetail.text('Waiting for the current GoClaw process to stop...');
                this.restartModal.show();
                await this.waitForGatewayAfterRestart(previousInstanceID);
                return;
            }

            if (apply.action === 'supervised_restart' && apply.waitForRestart) {
                this.completeModal.hide();
                this.$restartMessage.text(apply.message || 'Configuration saved. Waiting for GoClaw to restart...');
                this.$restartDetail.text('Waiting for the current GoClaw process to stop...');
                this.restartModal.show();
                await this.waitForGatewayAfterRestart(previousInstanceID);
                return;
            }

            this.appliedModal.show();
        }

        async waitForGatewayAfterRestart(previousInstanceID) {
            const result = await waitForGatewayRestart(previousInstanceID, (update) => {
                if (update.phase === 'waiting_for_stop') {
                    this.$restartDetail.text('Waiting for the current GoClaw process to stop...');
                } else if (update.phase === 'waiting_for_start') {
                    this.$restartDetail.text('Waiting for GoClaw to come back online...');
                }
                if (update.elapsedMs > 10000) {
                    if (update.phase === 'waiting_for_stop') {
                        this.$restartDetail.text('Still waiting for GoClaw to stop...');
                    } else {
                        this.$restartDetail.text('Still waiting for GoClaw to restart...');
                    }
                }
            });
            this.restartModal.hide();
            if (!result.ready) {
                throw new Error('Configuration saved, but GoClaw did not come back online automatically.');
            }
            this.lastKnownInstanceID = result.status && result.status.instanceID ? result.status.instanceID : this.lastKnownInstanceID;
            this.appliedModal.show();
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
