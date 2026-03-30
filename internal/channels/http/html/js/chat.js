/**
 * GoClaw web chat client. Requires window.__GOCLAW_CHAT_CONFIG__
 * { isSupervising, superviseSession, typingText } from chat.html.
 * Deps: jQuery, marked, DOMPurify, hljs, Bootstrap (page header).
 */
(function () {
    'use strict';

    $(document).ready(function () {
    var CFG = window.__GOCLAW_CHAT_CONFIG__ || {};
    var isSupervising = !!CFG.isSupervising;
    var superviseSession = (CFG.superviseSession != null && CFG.superviseSession !== undefined) ? String(CFG.superviseSession) : '';
    var typingText = (CFG.typingText != null && CFG.typingText !== '') ? String(CFG.typingText) : 'GoClaw is typing...';

    var eventSource = null;
    var reconnectTimer = null;
    var currentRunId = null;
    var $currentBubble = null;
    var currentRawText = ''; // Accumulate raw text for markdown rendering
    var $messages = $('#chat-messages');
    var $input = $('#message-input');
    var $sendBtn = $('#send-btn');
    var $status = $('#connection-status');

    function safeParseJSON(eventType, rawData) {
        if (rawData === undefined || rawData === null || rawData === '') {
            return { ok: true, value: null };
        }
        try {
            return { ok: true, value: JSON.parse(rawData) };
        } catch (err) {
            console.warn('goclaw chat: SSE protocol error', eventType, err);
            return { ok: false, error: err };
        }
    }
    function scrollChatToBottom(reason, options) {
        options = options || {};
        if (!options.force && historyHydrationActive()) {
            logDomCap('scroll-bottom-skipped', {
                reason: reason || 'unspecified',
                scrollTop: $messages.length ? $messages[0].scrollTop : 0,
                scrollHeight: $messages.length ? $messages[0].scrollHeight : 0
            });
            return;
        }
        requestAnimationFrame(function() {
            if ($messages.length) {
                $messages[0].scrollTop = $messages[0].scrollHeight;
            }
            if (typeof options.afterScroll === 'function') {
                options.afterScroll();
            }
        });
    }
    function updateComposerReserve() {
        var $bar = $('.chat-composer-bar');
        if (!$bar.length) return;
        var h = $bar.outerHeight(true);
        document.documentElement.style.setProperty('--chat-composer-reserve', Math.ceil(h + 12) + 'px');
    }
    function appendProtocolErrorBubble(eventType) {
        var label = eventType || 'event';
        var $msg = $('<div class="message error"><div class="bubble"></div></div>');
        $msg.find('.bubble').text('Protocol error (event: ' + label + ')');
        $messages.append($msg);
        announceToScreenReader('Chat protocol error.');
        trimChatDomIfNeeded();
        updateLoadEarlierVisibility();
        scrollChatToBottom();
    }
    
    // Supervision mode variables (must be before STORAGE_KEY)
    var llmEnabled = true; // LLM enabled state for supervised session
    var showDebug = false; // Whether to show debug content in supervision mode
    
    // Use different storage key for supervision sessions vs owner's own chat
    var STORAGE_KEY = isSupervising ? 'goclaw_supervise_' + superviseSession : 'goclaw_chat_history';
    var MAX_MESSAGES = 100; // Keep last 100 messages in localStorage snapshot
    /** Full transcript for persistence; DOM may show only last chatDomCap messages (P6). */
    var canonicalHistory = [];
    /** P6: max .message rows mounted in #chat-messages (storage may hold more). Override: ?chatDomCap= or ?domWindow= */
    var CHAT_DOM_CAP_DEFAULT = 120;
    var CHAT_DOM_CAP_MAX = 500;
    function initChatDomCap() {
        try {
            var params = new URLSearchParams(window.location.search);
            var q = params.get('chatDomCap') || params.get('domWindow');
            if (q === null || q === '') return CHAT_DOM_CAP_DEFAULT;
            var n = parseInt(q, 10);
            if (!isFinite(n) || n < 1) return CHAT_DOM_CAP_DEFAULT;
            return Math.min(n, CHAT_DOM_CAP_MAX);
        } catch (err) {
            return CHAT_DOM_CAP_DEFAULT;
        }
    }
    var chatDomCap = initChatDomCap();
    /** P6 full: prepend older canonical rows; scroll-to-top also triggers load (debounced). */
    var CHAT_LOAD_EARLIER_BATCH = 40;
    var CHAT_LOAD_EARLIER_SCROLL_DEBOUNCE_MS = 350;
    var CHAT_LOAD_EARLIER_MIN_GAP_MS = 1800;
    var CHAT_INITIAL_BACKFILL_BATCH = 20;
    var historyWindowStart = 0;
    var historyWindowEnd = -1;
    var deferHistoryRebuildAfterStream = false;
    var loadEarlierScrollTimer = null;
    var lastAutoPrependAt = 0;
    var loadEarlierInProgress = false;
    var initialHistoryHydrationActive = false;
    var historyBackfillInProgress = false;
    var initialHistoryBackfillTimer = null;
    var initialHistoryTargetStart = 0;
    var historyHydrationPhase = 'idle';

    function historyHydrationActive() {
        return initialHistoryHydrationActive || historyBackfillInProgress || !!initialHistoryBackfillTimer;
    }

    function mountedWindowLabel() {
        var $msgNodes = $messages.children('.message');
        if (!$msgNodes.length) return '[]';
        var firstIdx = $msgNodes.first().attr('data-canonical-index');
        var lastIdx = $msgNodes.last().attr('data-canonical-index');
        return '[' + String(firstIdx || '?') + '..' + String(lastIdx || '?') + ']';
    }

    function captureViewportMetrics() {
        var el = $messages.length ? $messages[0] : null;
        return {
            scrollY: el ? el.scrollTop : 0,
            scrollHeight: el ? el.scrollHeight : 0,
            viewportHeight: el ? el.clientHeight : 0
        };
    }

    function syncHistoryWindowFromDom(reason) {
        var $msgNodes = $messages.children('.message');
        if (!$msgNodes.length) {
            historyWindowStart = Math.max(0, Math.min(historyWindowStart, canonicalHistory.length));
            historyWindowEnd = historyWindowStart - 1;
            logDomCap('sync-window-empty', { reason: reason || 'unspecified' });
            return;
        }
        var firstIdx = parseInt($msgNodes.first().attr('data-canonical-index') || '', 10);
        var lastIdx = parseInt($msgNodes.last().attr('data-canonical-index') || '', 10);
        if (!isNaN(firstIdx)) {
            historyWindowStart = firstIdx;
        }
        if (!isNaN(lastIdx)) {
            historyWindowEnd = lastIdx;
        }
        logDomCap('sync-window', {
            reason: reason || 'unspecified',
            mountedWindow: mountedWindowLabel()
        });
    }

    function clearInitialHistoryBackfillTimer() {
        if (initialHistoryBackfillTimer) {
            clearTimeout(initialHistoryBackfillTimer);
            initialHistoryBackfillTimer = null;
        }
    }

    function updateInitialHistoryBottomAnchorLayout() {
        if (!$messages.length || !$messages.hasClass('initial-history-anchor')) {
            return;
        }
        logDomCap('initial-anchor-layout', {
            anchorHeight: $messages[0].clientHeight
        });
    }

    function enableInitialHistoryBottomAnchor(reason) {
        if (!$messages.length) return;
        $messages.addClass('initial-history-anchor');
        updateInitialHistoryBottomAnchorLayout();
        logDomCap('initial-anchor-enabled', { reason: reason || 'unspecified' });
    }

    function disableInitialHistoryBottomAnchor(reason) {
        if (!$messages.length) return;
        $messages.removeClass('initial-history-anchor');
        $messages.css('height', '');
        $messages.css('min-height', '');
        if ($messages.length) {
            $messages[0].scrollTop = $messages[0].scrollHeight;
        }
        logDomCap('initial-anchor-disabled', { reason: reason || 'unspecified' });
    }

    function finishInitialHistoryHydration(reason) {
        clearInitialHistoryBackfillTimer();
        initialHistoryHydrationActive = false;
        historyBackfillInProgress = false;
        historyHydrationPhase = 'idle';
        disableInitialHistoryBottomAnchor(reason);
        logDomCap('initial-hydration-complete', {
            reason: reason || 'unspecified',
            mountedWindow: mountedWindowLabel()
        });
    }

    function announceToScreenReader(shortMessage) {
        var el = document.getElementById('chat-announcer');
        if (!el || !shortMessage) return;
        el.textContent = '';
        requestAnimationFrame(function() {
            el.textContent = shortMessage;
        });
    }

    /** True while SSE stream has an active assistant bubble (never rebuild/evict mid-stream). */
    function streamingActive() {
        return !!(currentRunId && $currentBubble);
    }

    /** DevTools: filter `goclaw chat: dom-cap` to verify windowing / capping. */
    function logDomCap(phase, extra) {
        var row = {
            phase: phase,
            domMessageRows: $messages.children('.message').length,
            chatDomCap: chatDomCap,
            window: '[' + historyWindowStart + '..' + historyWindowEnd + ']',
            mountedWindow: mountedWindowLabel(),
            canonicalLen: canonicalHistory.length,
            maxStored: MAX_MESSAGES,
            streaming: streamingActive(),
            hydrationActive: historyHydrationActive(),
            hydrationPhase: historyHydrationPhase,
            hydrationTargetStart: initialHistoryTargetStart
        };
        if (extra && typeof extra === 'object') {
            for (var k in extra) {
                if (Object.prototype.hasOwnProperty.call(extra, k)) {
                    row[k] = extra[k];
                }
            }
        }
        console.log('goclaw chat: dom-cap', row);
    }

    /** Drop oldest .message rows when count exceeds chatDomCap; sync historyWindowStart from data-canonical-index when present. */
    function trimChatDomIfNeeded() {
        if (streamingActive()) {
            logDomCap('trim-skipped-streaming');
            return;
        }
        var $msgNodes = $messages.children('.message');
        if ($msgNodes.length <= chatDomCap) {
            updateLoadEarlierVisibility();
            return;
        }
        var excess = $msgNodes.length - chatDomCap;
        var ciBefore = $msgNodes.eq(excess).attr('data-canonical-index');
        logDomCap('trim-top', { excess: excess, firstKeptCanonicalIndex: ciBefore });
        var $firstKeep = $msgNodes.eq(excess);
        if (!$firstKeep.length) return;
        $firstKeep.prevAll().not('#chat-history-tools').remove();
        syncHistoryWindowFromDom('trim-top');
        updateLoadEarlierVisibility();
        logDomCap('trim-top-done', { historyWindowStart: historyWindowStart });
    }

    function persistCanonicalHistory() {
        try {
            localStorage.setItem(STORAGE_KEY, JSON.stringify(canonicalHistory));
        } catch (err) {
            console.error('Failed to save chat history:', err);
        }
    }

    function resetTransientRunDebugMaps() {
        // Keep debugByCanonicalIndex snapshots; reset only live run/panel instances
        // that point at current DOM nodes.
        runViewsByID = {};
        toolPanelsByRun = {};
        toolPanelOrder = [];
        toolAutoSeqByRun = {};
    }

    function shiftDebugIndexMap(dropCount) {
        if (!dropCount || dropCount <= 0) return;
        var shifted = {};
        Object.keys(debugByCanonicalIndex).forEach(function(k) {
            var idx = parseInt(k, 10);
            if (isNaN(idx)) return;
            var nextIdx = idx - dropCount;
            if (nextIdx >= 0) {
                shifted[String(nextIdx)] = debugByCanonicalIndex[k];
            }
        });
        debugByCanonicalIndex = shifted;
    }

    /**
     * Push one row into canonicalHistory. If storage cap slices, rebuild DOM from tail when safe.
     * @returns {boolean} true if DOM was fully rebuilt (caller must not also append the new bubble).
     */
    function appendCanonicalMessage(entry) {
        if (!entry || !entry.role) return false;
        canonicalHistory.push(entry);
        if (canonicalHistory.length > MAX_MESSAGES) {
            var over = canonicalHistory.length - MAX_MESSAGES;
            logDomCap('storage-slice', { droppedFromFront: over, role: entry.role });
            canonicalHistory = canonicalHistory.slice(-MAX_MESSAGES);
            shiftDebugIndexMap(over);
            historyWindowStart = Math.max(0, historyWindowStart - over);
            historyWindowEnd = canonicalHistory.length - 1;
            persistCanonicalHistory();
            if (streamingActive()) {
                deferHistoryRebuildAfterStream = true;
                logDomCap('storage-slice-deferred-rebuild');
                return false;
            }
            historyWindowStart = Math.max(0, historyWindowEnd - (chatDomCap - 1));
            rebuildHistoryDomFromCanonicalWindow();
            return true;
        }
        historyWindowEnd = canonicalHistory.length - 1;
        persistCanonicalHistory();
        return false;
    }

    function saveHistory() {
        persistCanonicalHistory();
    }

    var pendingImage = null; // { data: base64, mimeType: string, dataUrl: string }
    var pendingFiles = []; // File[] — POST /api/send/multipart (P3b)
    var typingTimeout = null; // For hiding typing indicator after inactivity
    var TYPING_TIMEOUT_MS = 5000; // Hide typing after 5s of no stream data
    var showThinking = false; // Whether to show tool calls and thinking output
    var currentThinkingRunId = null;
    var currentThinkingText = '';
    var $currentThinkingWrap = null;
    var $currentThinkingText = null;
    var lastStartedRunId = null;
    var runViewsByID = {};
    var debugByCanonicalIndex = {};
    var toolPanelsByRun = {};
    var toolPanelOrder = [];
    var toolAutoSeqByRun = {};
    var TOOL_PANEL_FILTERS = ['all', 'running', 'errors'];
    var TOOL_DEBUG_LOG = true;

    function toolDebugLog(label, data) {
        if (!TOOL_DEBUG_LOG || !window.console || typeof console.log !== 'function') return;
        try {
            console.log('goclaw chat: tool-debug ' + label, data || {});
        } catch (e) { /* ignore */ }
    }

    // P2a: streaming display — plain (default) vs debounced incremental markdown (§ CHATIMPROVEMENT)
    var STREAM_MARKDOWN_STORAGE = 'goclaw_chat_stream_markdown';
    var STREAM_MARKDOWN_DEBOUNCE_MS = 200;
    var STREAM_MARKDOWN_MODES = ['plain', 'debounced', 'token'];
    var streamMarkdownDebounceTimer = null;
    var STREAM_MODE_LABELS = {
        plain: 'Plain',
        debounced: 'Debounced MD',
        token: 'Token MD'
    };

    function initStreamMarkdownMode() {
        try {
            var params = new URLSearchParams(window.location.search);
            var q = (params.get('streamMarkdown') || '').toLowerCase();
            if (q === 'plain' || q === 'debounced' || q === 'token') {
                localStorage.setItem(STREAM_MARKDOWN_STORAGE, q);
                return q;
            }
        } catch (e) { /* ignore */ }
        try {
            var s = (localStorage.getItem(STREAM_MARKDOWN_STORAGE) || 'plain').toLowerCase();
            if (s === 'token') return 'token';
            if (s === 'debounced') return 'debounced';
        } catch (e) { /* ignore */ }
        return 'plain';
    }
    var streamMarkdownMode = initStreamMarkdownMode();

    function streamModeLabel(mode) {
        return STREAM_MODE_LABELS[mode] || STREAM_MODE_LABELS.plain;
    }

    function updateStreamModeUI() {
        var $toggle = $('#stream-mode-toggle');
        if ($toggle.length) {
            $toggle.text('Stream: ' + streamModeLabel(streamMarkdownMode));
            $toggle.attr('title', 'Streaming mode: ' + streamModeLabel(streamMarkdownMode));
        }
        $('#stream-mode-menu [data-stream-mode]').removeClass('active');
        $('#stream-mode-menu [data-stream-mode="' + streamMarkdownMode + '"]').addClass('active');
    }

    function applyCurrentStreamRender() {
        if (!$currentBubble) return;
        if (streamMarkdownMode === 'plain') {
            $currentBubble.text(currentRawText);
        } else if (streamMarkdownMode === 'debounced') {
            scheduleStreamMarkdownUpdate();
            return;
        } else {
            $currentBubble.html(renderMarkdown(currentRawText));
        }
        scrollChatToBottom();
    }

    function setStreamMarkdownMode(mode) {
        if (STREAM_MARKDOWN_MODES.indexOf(mode) === -1) return;
        streamMarkdownMode = mode;
        try {
            localStorage.setItem(STREAM_MARKDOWN_STORAGE, mode);
        } catch (e) { /* ignore */ }
        cancelStreamMarkdownDebounce();
        updateStreamModeUI();
        applyCurrentStreamRender();
    }

    function cancelStreamMarkdownDebounce() {
        if (streamMarkdownDebounceTimer !== null) {
            clearTimeout(streamMarkdownDebounceTimer);
            streamMarkdownDebounceTimer = null;
        }
    }
    function scheduleStreamMarkdownUpdate() {
        if (streamMarkdownMode !== 'debounced' || !$currentBubble) return;
        cancelStreamMarkdownDebounce();
        streamMarkdownDebounceTimer = setTimeout(function() {
            streamMarkdownDebounceTimer = null;
            if ($currentBubble && currentRawText !== undefined) {
                $currentBubble.html(renderMarkdown(currentRawText));
                scrollChatToBottom();
            }
        }, STREAM_MARKDOWN_DEBOUNCE_MS);
    }

    function renderMarkdownCompat(text) {
        if (typeof renderMarkdown === 'function') {
            return renderMarkdown(text);
        }
        return $('<div>').text(text || '').html();
    }

    function escapeHtmlCompat(text) {
        if (typeof escapeHtml === 'function') {
            return escapeHtml(text);
        }
        return $('<div>').text(text || '').html();
    }

    function resetThinkingStreamState(runId) {
        currentThinkingRunId = runId || null;
        currentThinkingText = '';
        $currentThinkingWrap = null;
        $currentThinkingText = null;
    }

    function ensureThinkingStreamBlock() {
        if ($currentThinkingText && $currentThinkingText.length) return;
        var runView = ensureRunView(currentThinkingRunId || currentRunId);
        if (!runView) return;
        var debugClass = isSupervising ? ' debug-content' : '';
        var $thinking = $('<div class="thinking-content' + debugClass + '">' +
            '<details open>' +
                '<summary><i class="bi bi-lightbulb"></i> Reasoning</summary>' +
                '<div class="thinking-text"></div>' +
            '</details>' +
        '</div>');
        runView.$debugHost.append($thinking);
        setRunDebugVisible(runView, true);
        $currentThinkingWrap = $thinking;
        $currentThinkingText = $thinking.find('.thinking-text');
    }

    function updateThinkingStreamText(content) {
        if (!isSupervising && !showThinking) return;
        if (!content) return;
        ensureThinkingStreamBlock();
        currentThinkingText = content;
        if ($currentThinkingText && $currentThinkingText.length) {
            $currentThinkingText.text(currentThinkingText);
        }
        scrollChatToBottom();
    }

    function appendThinkingDelta(delta, runId) {
        if (!isSupervising && !showThinking) return;
        if (!delta) return;
        if (runId && currentThinkingRunId && runId !== currentThinkingRunId) {
            resetThinkingStreamState(runId);
        }
        ensureThinkingStreamBlock();
        currentThinkingText += delta;
        if ($currentThinkingText && $currentThinkingText.length) {
            $currentThinkingText.text(currentThinkingText);
        }
        scrollChatToBottom();
    }

    function shouldAutoscrollToolActivity() {
        if (!$messages.length) return true;
        var el = $messages[0];
        var viewportBottom = el.scrollTop + el.clientHeight;
        var scrollBottom = el.scrollHeight;
        return (scrollBottom - viewportBottom) < 220;
    }

    function buildToolPreview(text, maxLen) {
        var value = String(text || '').replace(/\s+/g, ' ').trim();
        if (!value) return '';
        if (value.length <= maxLen) return value;
        return value.slice(0, maxLen - 3) + '...';
    }

    function getRunView(runId) {
        if (!runId) return null;
        return runViewsByID[String(runId)] || null;
    }

    function resolveRunIdFromData(data) {
        if (!data) return '';
        var rid = String(data.runId || data.RunID || '').trim();
        return rid;
    }

    function getCurrentRunIdFromDom() {
        if (!$currentBubble || !$currentBubble.length) return '';
        var rid = String($currentBubble.closest('.run-message').attr('data-run-id') || '').trim();
        return rid;
    }

    function resolveEventRunId(data) {
        var rid = resolveRunIdFromData(data);
        if (rid) return rid;
        if (currentRunId) return String(currentRunId);
        var domRid = getCurrentRunIdFromDom();
        if (domRid) return domRid;
        if (lastStartedRunId) return String(lastStartedRunId);
        return '';
    }

    function setRunDebugVisible(runView, visible) {
        if (!runView) return;
        if (visible) {
            runView.$debugHost.show();
        } else {
            runView.$debugHost.hide();
        }
    }

    function pinRunDebug(runView) {
        if (!runView) return;
        if (!runView.pinned) {
            runView.pinned = true;
        }
        var visible = runView.$debugHost.is(':visible');
        setRunDebugVisible(runView, visible);
    }

    function ensureRunView(runId) {
        var key = String(runId || currentRunId || getCurrentRunIdFromDom() || lastStartedRunId || '');
        if (!key) return null;
        var existing = getRunView(key);
        if (existing) {
            toolDebugLog('ensureRunView:existing', { key: key });
            return existing;
        }

        if ($currentBubble && $currentBubble.length) {
            var $msg = $currentBubble.closest('.message');
            if ($msg.length) {
                var $toggle = $msg.find('.run-debug-toggle');
                if (!$toggle.length) {
                    $toggle = $();
                }
                var $host = $msg.find('.run-debug-host');
                if (!$host.length) {
                    $host = $('<div class="run-debug-host" style="display:none;"></div>');
                    var $bubble = $msg.find('.bubble').first();
                    if ($bubble.length) {
                        $bubble.before($host);
                    } else {
                        $msg.append($host);
                    }
                }
                $msg.attr('data-run-id', key);
                var view = { runId: key, $message: $msg, $debugToggle: $toggle, $debugHost: $host, pinned: false };
                runViewsByID[key] = view;
                toolDebugLog('ensureRunView:created', {
                    key: key,
                    hasCurrentBubble: !!($currentBubble && $currentBubble.length),
                    runViewKeys: Object.keys(runViewsByID),
                });
                return view;
            }
        }
        toolDebugLog('ensureRunView:missing', {
            key: key,
            currentRunId: currentRunId,
            domRunId: getCurrentRunIdFromDom(),
            lastStartedRunId: lastStartedRunId,
        });
        return null;
    }

    function collapseRunDebugSections(runView) {
        if (!runView || !runView.$debugHost) return;
        runView.$debugHost.find('details').prop('open', false);
    }

    function finalizeRunDebugAfterDone(runId, keepOpen) {
        var runView = getRunView(runId);
        if (!runView) return;
        if (runView.$debugHost.children().length === 0) {
            runView.$debugHost.hide();
            return;
        }
        // Keep host visible if it has debug content; collapse only details when not pinned.
        if (keepOpen || runView.pinned) {
            setRunDebugVisible(runView, true);
        } else {
            setRunDebugVisible(runView, true);
            collapseRunDebugSections(runView);
        }
    }

    function findToolPanelForRun(runId, runView) {
        var key = String(runId || '');
        if (key && toolPanelsByRun[key]) {
            return toolPanelsByRun[key];
        }
        for (var i = 0; i < toolPanelOrder.length; i++) {
            var panelKey = toolPanelOrder[i];
            var panel = toolPanelsByRun[panelKey];
            if (!panel) continue;
            if (runView && panel.runView === runView) {
                return panel;
            }
        }
        return null;
    }

    function buildRunDebugState(runId) {
        var runView = getRunView(runId);
        if (!runView || !runView.$debugHost) return null;
        if (runView.$debugHost.children().length === 0) return null;

        var state = {
            runId: String(runId || ''),
            pinned: !!runView.pinned
        };

        var $thinking = runView.$debugHost.find('.thinking-content').first();
        if ($thinking.length) {
            state.thinking = {
                text: String($thinking.find('.thinking-text').text() || ''),
                open: !!$thinking.find('details').first().prop('open')
            };
        }

        var panel = findToolPanelForRun(runId, runView);
        if (panel) {
            var rows = [];
            for (var i = 0; i < panel.orderedKeys.length; i++) {
                var key = panel.orderedKeys[i];
                var row = panel.rowsByKey[key];
                if (!row) continue;
                var dur = 0;
                var durText = String(row.$duration.text() || '');
                var m = durText.match(/(\d+)/);
                if (m) dur = parseInt(m[1], 10) || 0;
                rows.push({
                    key: key,
                    toolName: row.toolName,
                    status: row.status,
                    durationMs: dur,
                    inputText: String(row.$item.find('.tool-activity-input').text() || ''),
                    outputText: String(row.$resultOutput.text() || ''),
                    open: !!row.$item.find('details').first().prop('open')
                });
            }
            state.tools = {
                filter: panel.filter || 'all',
                open: !!panel.$panel.find('details').first().prop('open'),
                rows: rows
            };
        }
        return state;
    }

    function persistRunDebugSnapshotToIndex(runId, canonicalIndex) {
        if (canonicalIndex === undefined || canonicalIndex === null || canonicalIndex < 0) return;
        var state = buildRunDebugState(runId);
        if (!state) {
            // Fallback: try the currently mounted run message key.
            var domRunId = getCurrentRunIdFromDom();
            if (domRunId && domRunId !== String(runId || '')) {
                state = buildRunDebugState(domRunId);
            }
        }
        if (!state) return;
        debugByCanonicalIndex[String(canonicalIndex)] = state;
    }

    function hydrateDebugForCanonicalIndex($message, canonicalIndex) {
        if (!$message || !$message.length) return;
        var snap = debugByCanonicalIndex[String(canonicalIndex)];
        if (!snap) return;
        var runId = String(snap.runId || ('hist-' + String(canonicalIndex)));
        $message.addClass('run-message');
        $message.attr('data-run-id', runId);
        var $bubble = $message.find('.bubble').first();
        var $host = $message.find('.run-debug-host');
        if (!$host.length) {
            $host = $('<div class="run-debug-host"></div>');
            if ($bubble.length) {
                $bubble.before($host);
            } else {
                $message.append($host);
            }
        }
        $host.empty().show();

        var runView = {
            runId: runId,
            $message: $message,
            $debugToggle: $(),
            $debugHost: $host,
            pinned: !!snap.pinned,
        };
        runViewsByID[runId] = runView;

        if (snap.thinking && snap.thinking.text) {
            var debugClass = isSupervising ? ' debug-content' : '';
            var $thinking = $('<div class="thinking-content' + debugClass + '">' +
                '<details>' +
                    '<summary><i class="bi bi-lightbulb"></i> Reasoning</summary>' +
                    '<div class="thinking-text"></div>' +
                '</details>' +
            '</div>');
            $thinking.find('.thinking-text').text(String(snap.thinking.text || ''));
            if (snap.thinking.open) {
                $thinking.find('details').prop('open', true);
            }
            $host.append($thinking);
        }

        if (snap.tools && Array.isArray(snap.tools.rows) && snap.tools.rows.length > 0) {
            var panel = createToolPanel(runView, runId);
            if (panel) {
                for (var i = 0; i < snap.tools.rows.length; i++) {
                    var rs = snap.tools.rows[i];
                    var key = String(rs.key || ('rehydrated:' + i));
                    var row = createToolRow(panel, key, rs.toolName || 'tool', rs.inputText || '');
                    if (rs.status === 'completed' || rs.status === 'error') {
                        updateToolRowResult(row, {
                            error: rs.status === 'error' ? (rs.outputText || 'error') : '',
                            displayResult: rs.status === 'error' ? '' : (rs.outputText || ''),
                            result: rs.status === 'error' ? '' : (rs.outputText || ''),
                            durationMs: rs.durationMs || 0,
                        });
                    } else {
                        row.status = 'running';
                    }
                    if (rs.open) {
                        row.$item.find('details').first().prop('open', true);
                    } else {
                        row.$item.find('details').first().prop('open', false);
                    }
                }
                panel.$panel.find('details').first().prop('open', !!snap.tools.open);
                setToolPanelFilter(panel, snap.tools.filter || 'all');
            }
        }
    }

    function ensureToolPanel(runId) {
        var key = String(runId || resolveEventRunId(null) || '');
        if (!key) return null;
        var panel = toolPanelsByRun[key];
        if (panel) return panel;
        var runView = ensureRunView(key);
        if (!runView) return null;
        return createToolPanel(runView, key);
    }

    function createToolPanel(runView, key) {
        if (!runView) return null;
        if (toolPanelsByRun[key]) return toolPanelsByRun[key];
        var debugClass = isSupervising ? ' debug-content' : '';
        var $panel = $('<div class="tool-activity-panel' + debugClass + '" data-run-id="' + escapeHtmlCompat(key) + '">' +
            '<details open>' +
                '<summary><i class="bi bi-tools me-1"></i> Tool Activity <span class="tool-activity-count">(0)</span></summary>' +
                '<div class="tool-activity-controls" role="group" aria-label="Filter tool activity">' +
                    '<button type="button" class="btn btn-sm btn-outline-secondary active" data-tool-filter="all">All</button>' +
                    '<button type="button" class="btn btn-sm btn-outline-secondary" data-tool-filter="running">Running</button>' +
                    '<button type="button" class="btn btn-sm btn-outline-secondary" data-tool-filter="errors">Errors</button>' +
                '</div>' +
                '<ul class="tool-activity-list"></ul>' +
                '<div class="tool-activity-empty text-muted small" style="display:none;">No entries for this filter.</div>' +
            '</details>' +
        '</div>');
        runView.$debugHost.append($panel);
        setRunDebugVisible(runView, true);

        var panel = {
            runId: key,
            $panel: $panel,
            $count: $panel.find('.tool-activity-count'),
            $controls: $panel.find('.tool-activity-controls'),
            $list: $panel.find('.tool-activity-list'),
            $empty: $panel.find('.tool-activity-empty'),
            rowsByKey: {},
            orderedKeys: [],
            filter: 'all',
            runView: runView,
        };
        toolPanelsByRun[key] = panel;
        toolPanelOrder.push(key);
        toolDebugLog('createToolPanel', {
            key: key,
            panelKeys: Object.keys(toolPanelsByRun),
            order: toolPanelOrder.slice(),
            runViewKeys: Object.keys(runViewsByID),
        });
        return panel;
    }

    function updateToolPanelCount(panel) {
        if (!panel || !panel.$count) return;
        panel.$count.text('(' + panel.orderedKeys.length + ')');
    }

    function updateToolFilterCounts(panel) {
        if (!panel || !panel.$controls || !panel.$controls.length) return;
        var total = panel.orderedKeys.length;
        var running = 0;
        var errors = 0;
        for (var i = 0; i < panel.orderedKeys.length; i++) {
            var key = panel.orderedKeys[i];
            var row = panel.rowsByKey[key];
            if (!row) continue;
            if (row.status === 'running') running++;
            if (row.status === 'error') errors++;
        }
        panel.$controls.find('[data-tool-filter="all"]').text('All (' + total + ')');
        panel.$controls.find('[data-tool-filter="running"]').text('Running (' + running + ')');
        panel.$controls.find('[data-tool-filter="errors"]').text('Errors (' + errors + ')');
    }

    function rowMatchesToolFilter(row, filter) {
        if (!row) return false;
        if (filter === 'running') return row.status === 'running';
        if (filter === 'errors') return row.status === 'error';
        return true;
    }

    function refreshToolPanelFilter(panel) {
        if (!panel) return;
        var visible = 0;
        for (var i = 0; i < panel.orderedKeys.length; i++) {
            var key = panel.orderedKeys[i];
            var row = panel.rowsByKey[key];
            if (!row || !row.$item) continue;
            var show = rowMatchesToolFilter(row, panel.filter);
            row.$item.toggle(show);
            if (show) visible++;
        }
        if (panel.$empty && panel.$empty.length) {
            panel.$empty.toggle(visible === 0);
        }
        if (panel.$controls && panel.$controls.length) {
            panel.$controls.find('[data-tool-filter]').removeClass('active');
            panel.$controls.find('[data-tool-filter="' + panel.filter + '"]').addClass('active');
        }
        updateToolFilterCounts(panel);
    }

    function setToolPanelFilter(panel, filter) {
        if (!panel) return;
        var next = String(filter || '').toLowerCase();
        if (TOOL_PANEL_FILTERS.indexOf(next) === -1) next = 'all';
        panel.filter = next;
        refreshToolPanelFilter(panel);
    }

    function createToolRow(panel, key, toolName, inputStr) {
        var preview = buildToolPreview(inputStr, 120);
        var $item = $('<li class="tool-activity-item" data-tool-key="' + escapeHtmlCompat(key) + '">' +
            '<details>' +
                '<summary>' +
                    '<span class="badge rounded-pill tool-activity-status running">running</span>' +
                    '<span class="tool-activity-name">' + escapeHtmlCompat(toolName || 'tool') + '</span>' +
                    '<span class="tool-activity-preview">' + escapeHtmlCompat(preview) + '</span>' +
                    '<span class="tool-activity-duration"></span>' +
                '</summary>' +
                '<div class="tool-activity-body">' +
                    '<div class="tool-activity-section-label">Arguments</div>' +
                    '<pre class="tool-activity-pre tool-activity-input"></pre>' +
                    '<div class="tool-activity-result-wrap" style="display:none;">' +
                        '<div class="tool-activity-section-label tool-activity-result-label">Result</div>' +
                        '<pre class="tool-activity-pre tool-activity-output"></pre>' +
                    '</div>' +
                '</div>' +
            '</details>' +
        '</li>');
        $item.find('.tool-activity-input').text(String(inputStr || ''));
        panel.$list.append($item);

        var row = {
            panel: panel,
            key: key,
            toolName: String(toolName || 'tool'),
            status: 'running',
            $item: $item,
            $status: $item.find('.tool-activity-status'),
            $preview: $item.find('.tool-activity-preview'),
            $duration: $item.find('.tool-activity-duration'),
            $resultWrap: $item.find('.tool-activity-result-wrap'),
            $resultLabel: $item.find('.tool-activity-result-label'),
            $resultOutput: $item.find('.tool-activity-output'),
        };
        panel.rowsByKey[key] = row;
        panel.orderedKeys.push(key);
        updateToolPanelCount(panel);
        refreshToolPanelFilter(panel);
        toolDebugLog('createToolRow', {
            runId: panel.runId,
            key: key,
            toolName: toolName,
            count: panel.orderedKeys.length,
        });
        return row;
    }

    function toolRowKeyForStart(panel, toolId, toolName) {
        if (toolId) return 'id:' + toolId;
        var runKey = panel.runId;
        var next = (toolAutoSeqByRun[runKey] || 0) + 1;
        toolAutoSeqByRun[runKey] = next;
        return 'auto:' + String(toolName || 'tool') + ':' + String(next);
    }

    function findRunningToolRowKey(panel, toolName) {
        if (!panel) return '';
        for (var i = 0; i < panel.orderedKeys.length; i++) {
            var key = panel.orderedKeys[i];
            var row = panel.rowsByKey[key];
            if (!row) continue;
            if (row.status === 'running' && row.toolName === String(toolName || '')) {
                return key;
            }
        }
        return '';
    }

    function findPanelForToolEnd(runId, toolId, toolName) {
        if (runId && toolPanelsByRun[runId]) {
            return toolPanelsByRun[runId];
        }
        for (var i = toolPanelOrder.length - 1; i >= 0; i--) {
            var key = toolPanelOrder[i];
            var panel = toolPanelsByRun[key];
            if (!panel) continue;
            if (toolId && panel.rowsByKey['id:' + toolId]) return panel;
            if (findRunningToolRowKey(panel, toolName)) return panel;
        }
        return null;
    }

    function updateToolRowResult(row, data) {
        if (!row || !data) return;
        var isError = !!data.error;
        row.status = isError ? 'error' : 'completed';
        row.$status.removeClass('running completed error').addClass(row.status).text(row.status);
        row.$duration.text(data.durationMs ? (String(data.durationMs) + 'ms') : '');

        var output = isError ? String(data.error || '') : String(data.displayResult || data.result || '');
        if (output) {
            row.$resultWrap.show();
            row.$resultLabel.text(isError ? 'Error' : 'Result');
            row.$resultOutput.text(output);
            row.$preview.text(buildToolPreview(output, 100));
        }
        var panel = row.panel;
        if (panel) {
            refreshToolPanelFilter(panel);
        }
    }

    // Build one .message element (used by history window, load earlier, rebuild)
    function buildMessageElement(role, content, imageUrl, metadata) {
        if (typeof window.buildMessageElement === 'function') {
            return window.buildMessageElement({
                role: role,
                content: content || '',
                imageUrl: imageUrl || '',
                metadata: metadata || {},
                useJQuery: true,
            });
        }
        // Should never happen because goclaw-common.js is loaded first.
        var $msg = $('<div class="message assistant"><div class="bubble"></div></div>');
        $msg.find('.bubble').append(renderMarkdownCompat(content || ''));
        return $msg;
    }

    function buildCanonicalRangeFragment(startIdx, endIdx) {
        var fragment = document.createDocumentFragment();
        var mounted = 0;
        var firstIndex = -1;
        var lastIndex = -1;
        for (var i = startIdx; i <= endIdx && i >= 0 && i < canonicalHistory.length; i++) {
            var msg = canonicalHistory[i];
            var $m = buildMessageElement(msg.role, msg.content, null, {
                supervisor: msg.supervisor,
                interventionType: msg.interventionType,
                source: msg.source
            });
            $m.attr('data-canonical-index', String(i));
            hydrateDebugForCanonicalIndex($m, i);
            fragment.appendChild($m[0]);
            mounted++;
            if (firstIndex === -1) {
                firstIndex = i;
            }
            lastIndex = i;
        }
        return {
            fragment: fragment,
            mounted: mounted,
            firstIndex: firstIndex,
            lastIndex: lastIndex
        };
    }

    function prependCanonicalRange(startIdx, endIdx, options) {
        options = options || {};
        var before = options.preserveViewport ? captureViewportMetrics() : null;
        var built = buildCanonicalRangeFragment(startIdx, endIdx);
        if (built.mounted === 0) {
            return {
                mounted: 0,
                scrollDelta: 0,
                droppedFromBottom: 0
            };
        }

        var $tools = $('#chat-history-tools');
        if ($tools.length) {
            $tools.after(built.fragment);
        } else {
            $messages.prepend(built.fragment);
        }

        syncHistoryWindowFromDom(options.reason || 'prepend-range');

        var droppedFromBottom = 0;
        if (options.enforceCap) {
            var over = $messages.children('.message').length - chatDomCap;
            if (over > 0) {
                droppedFromBottom = over;
                logDomCap('prepend-cap-overflow', {
                    reason: options.reason || 'prepend-range',
                    domRowsBeforeCap: $messages.children('.message').length,
                    dropFromBottom: over
                });
                removeBottomMessageBlocks(over);
            }
        }

        var scrollDelta = 0;
        if (before) {
            var after = captureViewportMetrics();
            scrollDelta = after.scrollHeight - before.scrollHeight;
            if ($messages.length) {
                $messages[0].scrollTop = before.scrollY + scrollDelta;
            }
        }

        updateLoadEarlierVisibility();
        return {
            mounted: built.mounted,
            firstIndex: built.firstIndex,
            lastIndex: built.lastIndex,
            scrollDelta: scrollDelta,
            droppedFromBottom: droppedFromBottom,
            domMessageRows: $messages.children('.message').length
        };
    }

    function scheduleInitialHistoryBackfill(delayMs) {
        if (!initialHistoryHydrationActive || historyBackfillInProgress || initialHistoryBackfillTimer) {
            return;
        }
        var delay = typeof delayMs === 'number' ? delayMs : 0;
        initialHistoryBackfillTimer = setTimeout(function() {
            initialHistoryBackfillTimer = null;
            if (!initialHistoryHydrationActive) {
                return;
            }
            if (streamingActive()) {
                historyHydrationPhase = 'paused-streaming';
                logDomCap('initial-backfill-paused-streaming');
                return;
            }

            var remaining = historyWindowStart - initialHistoryTargetStart;
            if (remaining <= 0) {
                finishInitialHistoryHydration('target-window-mounted');
                return;
            }

            var batch = Math.min(CHAT_INITIAL_BACKFILL_BATCH, remaining);
            var batchStart = historyWindowStart - batch;
            var batchEnd = historyWindowStart - 1;
            historyBackfillInProgress = true;
            historyHydrationPhase = 'initial-backfill';
            var before = captureViewportMetrics();
            logDomCap('initial-backfill-start', {
                batch: batch,
                batchStart: batchStart,
                batchEnd: batchEnd,
                scrollYBefore: before.scrollY,
                documentHeightBefore: before.scrollHeight
            });
            var result = prependCanonicalRange(batchStart, batchEnd, {
                preserveViewport: false,
                enforceCap: true,
                reason: 'initial-backfill'
            });
            updateInitialHistoryBottomAnchorLayout();
            if ($messages.length) {
                $messages[0].scrollTop = $messages[0].scrollHeight;
            }
            var after = captureViewportMetrics();
            historyBackfillInProgress = false;
            logDomCap('initial-backfill-done', {
                batchMounted: result.mounted,
                batchStart: result.firstIndex,
                batchEnd: result.lastIndex,
                droppedFromBottom: result.droppedFromBottom,
                scrollDelta: result.scrollDelta,
                scrollYAfter: after.scrollY,
                documentHeightAfter: after.scrollHeight
            });

            if (historyWindowStart <= initialHistoryTargetStart) {
                finishInitialHistoryHydration('target-window-mounted');
                return;
            }

            historyHydrationPhase = 'reverse-backfill-pending';
            scheduleInitialHistoryBackfill(delay);
        }, delay);

        logDomCap('initial-backfill-scheduled', {
            delayMs: delay,
            remainingRows: historyWindowStart - initialHistoryTargetStart
        });
    }

    function rebuildHistoryDomFromCanonicalWindow() {
        if (streamingActive()) return;
        var $typing = $('#typing-indicator').detach();
        var $tools = $('#chat-history-tools').detach();
        $messages.empty();
        resetTransientRunDebugMaps();
        if ($tools.length) {
            $messages.append($tools);
        }
        var built = buildCanonicalRangeFragment(historyWindowStart, historyWindowEnd);
        $messages.append(built.fragment);
        if ($typing.length) {
            $messages.append($typing);
        }
        syncHistoryWindowFromDom('rebuild-from-canonical');
        updateLoadEarlierVisibility();
        logDomCap('rebuild-from-canonical', { mountedRows: built.mounted });
    }

    function removeBottomMessageBlocks(n) {
        if (n <= 0) return;
        var endBefore = historyWindowEnd;
        logDomCap('drop-bottom-start', { removeCount: n, historyWindowEndBefore: endBefore });
        for (var r = 0; r < n; r++) {
            var $msgs = $messages.children('.message');
            if ($msgs.length === 0) break;
            var $last = $msgs.last();
            var $prev = $last.prevAll('.message').first();
            if ($prev.length) {
                $last.prevUntil($prev).addBack().remove();
            } else {
                $last.prevAll().not('#chat-history-tools').addBack().remove();
            }
        }
        syncHistoryWindowFromDom('drop-bottom');
        logDomCap('drop-bottom-done', { historyWindowEndAfter: historyWindowEnd });
    }

    function setLoadEarlierBusy(busy) {
        var $btn = $('#chat-load-earlier');
        var $sp = $('#chat-load-earlier-spinner');
        var $tx = $('#chat-load-earlier-text');
        if (!$btn.length) return;
        $btn.prop('disabled', !!busy);
        $btn.attr('aria-busy', busy ? 'true' : 'false');
        if (busy) {
            $btn.attr('aria-label', 'Loading earlier messages');
        } else {
            $btn.attr('aria-label', 'Load earlier chat messages');
        }
        if ($sp.length) {
            $sp.toggleClass('d-none', !busy);
        }
        if ($tx.length) {
            $tx.text(busy ? 'Loading…' : 'Load earlier messages');
        }
    }

    function prependHistoryBatch(batchSize) {
        batchSize = batchSize || CHAT_LOAD_EARLIER_BATCH;
        if (historyWindowStart <= 0 || canonicalHistory.length === 0) {
            updateLoadEarlierVisibility();
            return;
        }
        if (streamingActive()) return;
        if (loadEarlierInProgress) return;

        loadEarlierInProgress = true;
        setLoadEarlierBusy(true);

        setTimeout(function() {
            try {
                var batch = Math.min(batchSize, historyWindowStart);
                var winBefore = historyWindowStart + '..' + historyWindowEnd;
                var before = captureViewportMetrics();
                logDomCap('prepend-start', {
                    batch: batch,
                    windowBefore: winBefore,
                    scrollYBefore: before.scrollY,
                    documentHeightBefore: before.scrollHeight
                });
                var result = prependCanonicalRange(historyWindowStart - batch, historyWindowStart - 1, {
                    preserveViewport: true,
                    enforceCap: true,
                    reason: 'user-prepend'
                });
                var after = captureViewportMetrics();
                updateLoadEarlierVisibility();
                logDomCap('prepend-done', {
                    windowAfter: historyWindowStart + '..' + historyWindowEnd,
                    domMessageRows: $messages.children('.message').length,
                    batchMounted: result.mounted,
                    batchStart: result.firstIndex,
                    batchEnd: result.lastIndex,
                    droppedFromBottom: result.droppedFromBottom,
                    scrollDelta: result.scrollDelta,
                    scrollYAfter: after.scrollY,
                    documentHeightAfter: after.scrollHeight
                });
            } finally {
                loadEarlierInProgress = false;
                setLoadEarlierBusy(false);
            }
        }, 0);
    }

    function updateLoadEarlierVisibility() {
        var $wrap = $('#chat-history-tools');
        if (!$wrap.length) return;
        if (historyWindowStart > 0 && canonicalHistory.length > 0) {
            $wrap.show();
        } else {
            $wrap.hide();
        }
    }

    // Load chat history — hydrate canonical from the actual last message first.
    function loadHistory() {
        try {
            var parsed = JSON.parse(localStorage.getItem(STORAGE_KEY) || '[]');
            canonicalHistory = Array.isArray(parsed) ? parsed : [];
            clearInitialHistoryBackfillTimer();
            initialHistoryHydrationActive = false;
            historyBackfillInProgress = false;
            historyHydrationPhase = 'idle';
            resetTransientRunDebugMaps();

            var lastIndex = canonicalHistory.length - 1;
            historyWindowEnd = lastIndex;
            initialHistoryTargetStart = Math.max(0, canonicalHistory.length - chatDomCap);
            if (canonicalHistory.length === 0) {
                historyWindowStart = 0;
                disableInitialHistoryBottomAnchor('load-history-empty');
                updateLoadEarlierVisibility();
                logDomCap('load-history-empty');
                return;
            }

            historyWindowStart = lastIndex;
            initialHistoryHydrationActive = historyWindowStart > initialHistoryTargetStart;
            historyHydrationPhase = initialHistoryHydrationActive ? 'last-message-mounted' : 'idle';

            var $typing = $('#typing-indicator').detach();
            var $tools = $('#chat-history-tools').detach();
            enableInitialHistoryBottomAnchor('load-history');
            $messages.empty();
            if ($tools.length) {
                $messages.append($tools);
            }
            var built = buildCanonicalRangeFragment(historyWindowStart, historyWindowEnd);
            $messages.append(built.fragment);
            if ($typing.length) {
                $messages.append($typing);
            }
            updateInitialHistoryBottomAnchorLayout();
            if ($messages.length) {
                $messages[0].scrollTop = $messages[0].scrollHeight;
            }
            syncHistoryWindowFromDom('load-history-last-message-first');
            updateLoadEarlierVisibility();
            logDomCap('load-history-last-mounted', {
                mountedFromIndex: historyWindowStart,
                mountedRows: built.mounted,
                targetStart: initialHistoryTargetStart,
                lastIndex: lastIndex
            });
            if (initialHistoryHydrationActive) {
                scheduleInitialHistoryBackfill(120);
            } else {
                finishInitialHistoryHydration('last-message-covered-target');
            }
        } catch (err) {
            console.error('Failed to load chat history:', err);
            canonicalHistory = [];
            historyWindowStart = 0;
            historyWindowEnd = -1;
            initialHistoryTargetStart = 0;
            finishInitialHistoryHydration('load-history-error');
        }
    }

    // Clear chat history
    function clearHistory() {
        localStorage.removeItem(STORAGE_KEY);
        clearInitialHistoryBackfillTimer();
        canonicalHistory = [];
        debugByCanonicalIndex = {};
        resetTransientRunDebugMaps();
        historyWindowStart = 0;
        historyWindowEnd = -1;
        initialHistoryTargetStart = 0;
        initialHistoryHydrationActive = false;
        historyBackfillInProgress = false;
        historyHydrationPhase = 'idle';
        disableInitialHistoryBottomAnchor('clear-history');
        var $typing = $('#typing-indicator').detach();
        var $tools = $('#chat-history-tools').detach();
        $messages.empty();
        if ($tools.length) {
            $messages.append($tools);
        }
        if ($typing.length) {
            $messages.append($typing);
        }
        updateLoadEarlierVisibility();
    }

    // Render a message; if save, push to canonical (returns rebuilt=true replaces entire thread DOM)
    function renderMessage(role, content, save, imageUrl, metadata) {
        metadata = metadata || {};
        var $msg = buildMessageElement(role, content, imageUrl, metadata);
        if (save !== false) {
            var entry = { role: role, content: content || '' };
            if (metadata.supervisor) entry.supervisor = metadata.supervisor;
            if (metadata.interventionType) entry.interventionType = metadata.interventionType;
            if (metadata.source) entry.source = metadata.source;
            var rebuilt = appendCanonicalMessage(entry);
            if (rebuilt) {
                return $messages.children('.message').last();
            }
            $msg.attr('data-canonical-index', String(canonicalHistory.length - 1));
        }
        $messages.append($msg);
        trimChatDomIfNeeded();
        updateLoadEarlierVisibility();
        return $msg;
    }

    // Connect to SSE
    function connect() {
        // Close existing connection if any
        if (eventSource) {
            eventSource.close();
            eventSource = null;
        }
        if (reconnectTimer) {
            clearTimeout(reconnectTimer);
            reconnectTimer = null;
        }
        
        $status.text('Connecting...').removeClass('bg-success bg-danger').addClass('bg-secondary');
        
        // Use supervision SSE endpoint if supervising
        var eventsUrl = isSupervising 
            ? '/api/sessions/' + encodeURIComponent(superviseSession) + '/events'
            : '/api/events';
        eventSource = new EventSource(eventsUrl);
        
        eventSource.addEventListener('connected', function(e) {
            $status.text('Connected').removeClass('bg-secondary bg-danger').addClass('bg-success');
            if (!isSupervising || !e.data) return;
            var parsed = safeParseJSON(e.type, e.data);
            if (!parsed.ok) {
                appendProtocolErrorBubble(e.type);
                return;
            }
            var data = parsed.value;
            if (data && typeof data.llmEnabled !== 'undefined') {
                llmEnabled = data.llmEnabled;
                updateLLMStatusUI();
            }
        });
        
        // Handle history messages in supervision mode
        eventSource.addEventListener('history', function(e) {
            if (!isSupervising) return;
            var parsed = safeParseJSON(e.type, e.data);
            if (!parsed.ok) {
                appendProtocolErrorBubble(e.type);
                return;
            }
            var data = parsed.value;
            if (!data) return;
            
            // Build metadata for supervision tracking
            var metadata = {
                supervisor: data.supervisor,
                interventionType: data.interventionType,
                source: data.source
            };
            
            // Detect supervision messages by interventionType or source
            var isGuidance = data.interventionType === 'guidance' || data.source === 'guidance';
            var isGhostwrite = data.interventionType === 'ghostwrite' || data.source === 'ghostwrite';
            
            // Render based on role and supervision status
            if (isGuidance) {
                renderMessage('guidance', data.content, false, null, metadata);
            } else if (isGhostwrite) {
                renderMessage('ghostwrite', data.content, false, null, metadata);
            } else if (data.role === 'user') {
                renderMessage('user', data.content, false, null, metadata);
            } else if (data.role === 'assistant') {
                renderMessage('assistant', data.content, false, null, metadata);
            } else if (data.role === 'tool_use' || data.role === 'tool_result') {
                // Tool messages are debug content
                var $tool = $('<div class="tool-call debug-content">' +
                    '<div class="tool-call-header">' +
                    '<span class="tool-call-icon"><i class="bi bi-gear"></i></span>' +
                    '<span class="tool-call-name">' + escapeHtml(data.toolName || data.role) + '</span>' +
                    '</div>' +
                    '<div class="tool-call-input">' + escapeHtml(data.content || '') + '</div>' +
                    '</div>');
                $messages.append($tool);
            }
            scrollChatToBottom();
        });
        
        eventSource.addEventListener('start', function(e) {
            var parsed = safeParseJSON(e.type, e.data);
            if (!parsed.ok) {
                appendProtocolErrorBubble(e.type);
                return;
            }
            var data = parsed.value;
            if (!data) return;
            currentRunId = resolveRunIdFromData(data);
            if (!currentRunId) {
                currentRunId = String(Date.now());
            }
            lastStartedRunId = currentRunId;
            toolDebugLog('event:start', {
                runId: currentRunId,
                payloadRunId: resolveRunIdFromData(data),
            });
            currentRawText = ''; // Reset raw text accumulator
            resetThinkingStreamState(currentRunId);
            cancelStreamMarkdownDebounce();
            // Always create bubble for streaming output; thinking stream is independent.
            var $msg = $('<div class="message assistant run-message" data-run-id="' + escapeHtmlCompat(currentRunId || '') + '">' +
                '<div class="run-debug-host" style="display:none;"></div>' +
                '<div class="bubble"></div>' +
            '</div>');
            $currentBubble = $msg.find('.bubble');
            $messages.append($msg);
            if (currentRunId) {
                runViewsByID[String(currentRunId)] = {
                    runId: String(currentRunId),
                    $message: $msg,
                    $debugToggle: $(),
                    $debugHost: $msg.find('.run-debug-host'),
                    pinned: false,
                };
            }
            trimChatDomIfNeeded();
            showTypingIndicator();
        });
        
        eventSource.addEventListener('message', function(e) {
            var parsed = safeParseJSON(e.type, e.data);
            if (!parsed.ok) {
                appendProtocolErrorBubble(e.type);
                return;
            }
            var data = parsed.value;
            if (!data || data.content === undefined) return;
            // Show typing indicator and reset timeout on each stream chunk
            showTypingIndicator();
            resetTypingTimeout();
            
            // Always accumulate raw text
            currentRawText += data.content;
            // Stream to bubble regardless of thinking mode.
            if ($currentBubble) {
                if (streamMarkdownMode === 'debounced') {
                    scheduleStreamMarkdownUpdate();
                } else if (streamMarkdownMode === 'token') {
                    $currentBubble.html(renderMarkdown(currentRawText));
                    scrollChatToBottom();
                } else {
                    $currentBubble.text(currentRawText);
                    scrollChatToBottom();
                }
            }
        });
        
        eventSource.addEventListener('done', function(e) {
            cancelStreamMarkdownDebounce();
            clearTypingTimeout();
            hideTypingIndicator();
            var parsed = safeParseJSON(e.type, e.data);
            if (!parsed.ok) {
                appendProtocolErrorBubble(e.type);
                return;
            }
            var data = parsed.value;
            if (!data) return;
            var completedRunId = resolveEventRunId(data);
            var domRunId = getCurrentRunIdFromDom();
            toolDebugLog('event:done:before-reconcile', {
                payloadRunId: resolveRunIdFromData(data),
                resolvedRunId: completedRunId,
                domRunId: domRunId,
                currentRunId: currentRunId,
                panelKeys: Object.keys(toolPanelsByRun),
                runViewKeys: Object.keys(runViewsByID),
            });
            if (domRunId && completedRunId && domRunId !== completedRunId) {
                // Reconcile mismatched run IDs so debug snapshot/finalization stays attached to one run.
                if (toolPanelsByRun[completedRunId] && !toolPanelsByRun[domRunId]) {
                    toolPanelsByRun[domRunId] = toolPanelsByRun[completedRunId];
                    delete toolPanelsByRun[completedRunId];
                }
                if (runViewsByID[completedRunId] && !runViewsByID[domRunId]) {
                    runViewsByID[domRunId] = runViewsByID[completedRunId];
                    delete runViewsByID[completedRunId];
                }
                completedRunId = domRunId;
            } else if (domRunId && !completedRunId) {
                completedRunId = domRunId;
            }
            toolDebugLog('event:done:after-reconcile', {
                completedRunId: completedRunId,
                panelKeys: Object.keys(toolPanelsByRun),
                runViewKeys: Object.keys(runViewsByID),
            });
            // Use finalText from server (has media refs enriched) instead of accumulated deltas
            var finalText = data.finalText || currentRawText;
            var completedCanonicalIndex = -1;

            if (finalText && String(finalText).trim().length > 0) {
                announceToScreenReader('Assistant reply complete.');
                var rebuiltDone = appendCanonicalMessage({ role: 'assistant', content: finalText });
                completedCanonicalIndex = canonicalHistory.length - 1;
                persistRunDebugSnapshotToIndex(completedRunId, completedCanonicalIndex);
                if (!rebuiltDone && $currentBubble) {
                    $currentBubble.html(renderMarkdown(finalText));
                    $currentBubble.closest('.message').attr('data-canonical-index', String(completedCanonicalIndex));
                    $currentBubble.closest('.message').data('raw-content', finalText);
                } else if (rebuiltDone) {
                    var $rebuiltLast = $messages.children('.message').last();
                    if ($rebuiltLast.length) {
                        $rebuiltLast.attr('data-canonical-index', String(completedCanonicalIndex));
                        hydrateDebugForCanonicalIndex($rebuiltLast, completedCanonicalIndex);
                    }
                }
            }
            currentRunId = null;
            currentRawText = '';
            $currentBubble = null;
            resetThinkingStreamState(null);
            finalizeRunDebugAfterDone(completedRunId, true);
            if (deferHistoryRebuildAfterStream) {
                deferHistoryRebuildAfterStream = false;
                historyWindowEnd = canonicalHistory.length - 1;
                historyWindowStart = Math.max(0, canonicalHistory.length - chatDomCap);
                rebuildHistoryDomFromCanonicalWindow();
            } else {
                trimChatDomIfNeeded();
            }
            if (initialHistoryHydrationActive) {
                historyHydrationPhase = 'reverse-backfill-pending';
                scheduleInitialHistoryBackfill(120);
            }
            updateLoadEarlierVisibility();
        });
        
        eventSource.addEventListener('mirror', function(e) {
            var parsed = safeParseJSON(e.type, e.data);
            if (!parsed.ok) {
                appendProtocolErrorBubble(e.type);
                return;
            }
            var data = parsed.value;
            if (!data) return;
            // Show mirrored user message from another channel (with source label)
            if (data.userMsg) {
                appendMessage('mirror-user', '[' + data.source + '] ' + data.userMsg);
            }
        });
        
        eventSource.addEventListener('agent_error', function(e) {
            clearTypingTimeout();
            hideTypingIndicator();
            var parsed = safeParseJSON(e.type, e.data);
            if (!parsed.ok) {
                appendProtocolErrorBubble(e.type);
                return;
            }
            var data = parsed.value;
            if (!data) return;
            var erroredRunId = resolveEventRunId(data);
            appendMessage('error', 'Error: ' + data.error);
            announceToScreenReader('Agent error.');
            currentRunId = null;
            $currentBubble = null;
            resetThinkingStreamState(null);
            finalizeRunDebugAfterDone(erroredRunId, true);
            if (initialHistoryHydrationActive) {
                historyHydrationPhase = 'reverse-backfill-pending';
                scheduleInitialHistoryBackfill(120);
            }
        });
        
        eventSource.addEventListener('agent_message', function(e) {
            var parsed = safeParseJSON(e.type, e.data);
            if (!parsed.ok) {
                appendProtocolErrorBubble(e.type);
                return;
            }
            var data = parsed.value;
            if (!data) return;
            if (data.message) {
                // Direct agent output (from DeliverMessage - tool messages, etc.)
                appendMessage('assistant', data.message);
            } else if (data.type === 'text') {
                appendMessage('assistant', data.text);
            } else if (data.type === 'media') {
                // Show media with optional caption
                var $msg = $('<div class="message assistant"><div class="bubble media-bubble"></div></div>');
                var $bubble = $msg.find('.bubble');
                var $img = $('<img>').attr('src', data.url).attr('alt', data.filename || 'Media');
                $img.on('load', function() {
                    scrollChatToBottom();
                });
                $img.on('error', function() {
                    $img.replaceWith($('<span class="text-muted">Failed to load image</span>'));
                });
                $bubble.append($img);
                if (data.caption) {
                    $bubble.append($('<div class="media-caption">').text(data.caption));
                }
                var mediaLine = (data.caption || '').trim() || (data.filename || 'Media');
                if (data.url) {
                    mediaLine += '\n' + data.url;
                }
                var rebuiltMedia = appendCanonicalMessage({ role: 'assistant', content: mediaLine });
                if (!rebuiltMedia) {
                    $messages.append($msg);
                    $msg.attr('data-canonical-index', String(canonicalHistory.length - 1));
                }
                trimChatDomIfNeeded();
                scrollChatToBottom();
            }
        });
        
        // Handle preference updates
        eventSource.addEventListener('preference', function(e) {
            var parsed = safeParseJSON(e.type, e.data);
            if (!parsed.ok) {
                appendProtocolErrorBubble(e.type);
                return;
            }
            var data = parsed.value;
            if (!data) return;
            if (data.key === 'thinking') {
                showThinking = data.value;
                updateThinkingButton(showThinking);
            }
        });
        
        // Handle system messages
        eventSource.addEventListener('system', function(e) {
            var parsed = safeParseJSON(e.type, e.data);
            if (!parsed.ok) {
                appendProtocolErrorBubble(e.type);
                return;
            }
            var data = parsed.value;
            if (!data) return;
            appendMessage('system', data.message);
        });
        
        // Handle user_message (real-time user messages in supervision mode)
        eventSource.addEventListener('user_message', function(e) {
            if (!isSupervising) return;
            var parsed = safeParseJSON(e.type, e.data);
            if (!parsed.ok) {
                appendProtocolErrorBubble(e.type);
                return;
            }
            var data = parsed.value;
            if (!data) return;
            if (data.content) {
                // Build metadata for supervision tracking
                var metadata = {
                    supervisor: data.supervisor,
                    interventionType: data.source, // source field contains intervention type for supervision events
                    source: data.source
                };
                
                // Check if this is a supervision intervention (guidance or ghostwrite)
                if (data.source === 'guidance' || data.source === 'ghostwrite') {
                    var roleClass = data.source; // 'guidance' or 'ghostwrite'
                    // Content will get the label prefix in renderMessage based on metadata
                    appendMessage(roleClass, data.content, null, metadata);
                } else {
                    // Regular user message with source indicator
                    var displayContent = data.source ? '[' + data.source + '] ' + data.content : data.content;
                    appendMessage('user', displayContent, null, metadata);
                }
                // appendCanonicalMessage via appendMessage -> renderMessage
            }
        });
        
        // Handle tool start (supervision: always render with debug-content class; normal: only if showThinking)
        eventSource.addEventListener('tool_start', function(e) {
            var parsed = safeParseJSON(e.type, e.data);
            if (!parsed.ok) {
                appendProtocolErrorBubble(e.type);
                return;
            }
            var data = parsed.value;
            if (!data) return;
            var toolId = data.toolId;
            var inputStr = typeof data.input === 'string' ? data.input : JSON.stringify(data.input, null, 2);
            
            var runId = resolveEventRunId(data);
            toolDebugLog('event:tool_start', {
                toolName: data.toolName,
                toolId: toolId,
                payloadRunId: resolveRunIdFromData(data),
                resolvedRunId: runId,
                currentRunId: currentRunId,
                domRunId: getCurrentRunIdFromDom(),
                lastStartedRunId: lastStartedRunId,
                panelKeys: Object.keys(toolPanelsByRun),
            });
            var panel = ensureToolPanel(runId);
            if (!panel) return;
            var rowKey = toolRowKeyForStart(panel, toolId, data.toolName);
            createToolRow(panel, rowKey, data.toolName, inputStr);
            if (shouldAutoscrollToolActivity()) {
                scrollChatToBottom();
            }
        });
        
        // Handle tool end (supervision: always render with debug-content class; normal: only if showThinking)
        eventSource.addEventListener('tool_end', function(e) {
            var parsed = safeParseJSON(e.type, e.data);
            if (!parsed.ok) {
                appendProtocolErrorBubble(e.type);
                return;
            }
            var data = parsed.value;
            if (!data) return;
            var toolId = data.toolId;

            var endRunId = resolveEventRunId(data);
            toolDebugLog('event:tool_end:lookup', {
                toolName: data.toolName,
                toolId: toolId,
                payloadRunId: resolveRunIdFromData(data),
                resolvedRunId: endRunId,
                panelKeys: Object.keys(toolPanelsByRun),
            });
            var panel = findPanelForToolEnd(endRunId, toolId, data.toolName);
            if (!panel) {
                toolDebugLog('event:tool_end:no-panel', {
                    toolName: data.toolName,
                    toolId: toolId,
                    resolvedRunId: endRunId,
                });
                return;
            }

            var row = null;
            if (toolId && panel.rowsByKey['id:' + toolId]) {
                row = panel.rowsByKey['id:' + toolId];
            } else {
                var fallbackKey = findRunningToolRowKey(panel, data.toolName);
                if (fallbackKey) {
                    row = panel.rowsByKey[fallbackKey];
                }
            }
            if (!row) {
                toolDebugLog('event:tool_end:no-row', {
                    runId: panel.runId,
                    toolName: data.toolName,
                    toolId: toolId,
                    knownRowKeys: Object.keys(panel.rowsByKey),
                    orderedKeys: panel.orderedKeys.slice(),
                });
                return;
            }

            updateToolRowResult(row, data);
            toolDebugLog('event:tool_end:updated', {
                runId: panel.runId,
                rowKey: row.key,
                status: row.status,
            });
            if (shouldAutoscrollToolActivity()) {
                scrollChatToBottom();
            }
        });
        
        // Handle thinking content (supervision: always render with debug-content class; normal: only if showThinking)
        eventSource.addEventListener('thinking', function(e) {
            // In normal mode, skip if thinking disabled
            if (!isSupervising && !showThinking) return;
            
            var parsed = safeParseJSON(e.type, e.data);
            if (!parsed.ok) {
                appendProtocolErrorBubble(e.type);
                return;
            }
            var data = parsed.value;
            if (!data) return;
            var thinkingRunId = resolveEventRunId(data);
            if (thinkingRunId) {
                currentThinkingRunId = thinkingRunId;
            }
            updateThinkingStreamText(data.content || '');
        });

        // Handle streaming thinking deltas
        eventSource.addEventListener('thinking_delta', function(e) {
            if (!isSupervising && !showThinking) return;
            var parsed = safeParseJSON(e.type, e.data);
            if (!parsed.ok) {
                appendProtocolErrorBubble(e.type);
                return;
            }
            var data = parsed.value;
            if (!data) return;
            appendThinkingDelta(data.content || '', resolveEventRunId(data));
        });
        
        eventSource.onerror = function(e) {
            // EventSource has built-in reconnection - don't fight it
            // readyState: 0=CONNECTING, 1=OPEN, 2=CLOSED
            if (eventSource.readyState === EventSource.CONNECTING) {
                // Auto-reconnecting - just show status
                $status.text('Reconnecting...').removeClass('bg-success').addClass('bg-warning');
            } else if (eventSource.readyState === EventSource.CLOSED) {
                // Fully closed - need manual reconnect
                $status.text('Disconnected').removeClass('bg-success bg-warning').addClass('bg-danger');
                eventSource = null;
                reconnectTimer = setTimeout(connect, 3000);
            }
        };
    }
    
    function appendMessage(role, content, imageUrl, metadata) {
        renderMessage(role, content, true, imageUrl, metadata);
        scrollChatToBottom();
    }
    
    function showTypingIndicator() {
        if ($('#typing-indicator').length === 0) {
            $messages.append('<div id="typing-indicator" class="typing-indicator" aria-hidden="true">' +
                '<span class="dots" aria-hidden="true"><span></span><span></span><span></span></span> ' + escapeHtml(typingText) + '</div>');
            scrollChatToBottom();
        }
    }
    
    function hideTypingIndicator() {
        $('#typing-indicator').remove();
    }
    
    function resetTypingTimeout() {
        clearTypingTimeout();
        typingTimeout = setTimeout(function() {
            hideTypingIndicator();
        }, TYPING_TIMEOUT_MS);
    }
    
    function clearTypingTimeout() {
        if (typingTimeout) {
            clearTimeout(typingTimeout);
            typingTimeout = null;
        }
    }
    
    // Keyboard handling for textarea
    $input.on('keydown', function(e) {
        if (e.key === 'Enter' && !e.shiftKey) {
            // Enter without Shift = submit
            e.preventDefault();
            $('#chat-form').submit();
        }
        // Shift+Enter = default behavior (newline)
    });
    
    // Auto-resize textarea
    $input.on('input', function() {
        this.style.height = 'auto';
        this.style.height = Math.min(this.scrollHeight, 150) + 'px';
        updateComposerReserve();
    });
    
    function renderPendingFilesList() {
        var $row = $('#pending-files-row');
        var $list = $('#pending-files-list');
        if (!pendingFiles.length) {
            $row.hide();
            $list.empty();
            return;
        }
        $row.show();
        $list.empty();
        pendingFiles.forEach(function(f) {
            var $chip = $('<span class="badge bg-secondary me-1 mb-1 align-middle"></span>');
            $chip.append($('<span></span>').text((f.name || 'file') + ' '));
            var $x = $('<button type="button" class="btn-close btn-close-white" style="font-size:0.55rem" aria-label="Remove"></button>');
            $x.on('click', function() {
                var i = pendingFiles.indexOf(f);
                if (i >= 0) pendingFiles.splice(i, 1);
                renderPendingFilesList();
                updateComposerReserve();
            });
            $chip.append($x);
            $list.append($chip);
        });
    }

    // Send message
    $('#chat-form').on('submit', function(e) {
        e.preventDefault();
        var message = $input.val().trim();
        
        var hasAttachments = pendingImage !== null || pendingFiles.length > 0;
        if (!message && !hasAttachments) return;
        
        // In supervision mode, handle guidance/ghostwrite differently
        if (isSupervising) {
            var sendMode = $('input[name="send-mode"]:checked').val() || 'guidance';
            var endpoint = sendMode === 'ghostwrite' 
                ? '/api/sessions/' + encodeURIComponent(superviseSession) + '/message'
                : '/api/sessions/' + encodeURIComponent(superviseSession) + '/guidance';
            
            // Clear input
            $input.val('');
            $input.css('height', 'auto');
            $sendBtn.prop('disabled', true);
            
            // Show what we're sending (differently styled for supervision)
            appendMessage(sendMode === 'ghostwrite' ? 'ghostwrite' : 'guidance', message);
            
            $.ajax({
                url: endpoint,
                method: 'POST',
                contentType: 'application/json',
                data: JSON.stringify({ content: message }),
                success: function(data) {
                    console.log('Supervision action sent:', data);
                },
                error: function(xhr) {
                    appendMessage('error', 'Failed to send: ' + xhr.responseText);
                },
                complete: function() {
                    $sendBtn.prop('disabled', false);
                }
            });
            return;
        }
        
        // Normal mode - build optional multipart file list (pasted image → File)
        var imageUrl = pendingImage ? pendingImage.dataUrl : null;
        var filesToSend = pendingFiles.slice();
        if (pendingImage) {
            try {
                var bin = atob(pendingImage.data);
                var arr = new Uint8Array(bin.length);
                for (var pi = 0; pi < bin.length; pi++) {
                    arr[pi] = bin.charCodeAt(pi);
                }
                var blob = new Blob([arr], { type: pendingImage.mimeType || 'application/octet-stream' });
                var sub = (pendingImage.mimeType && pendingImage.mimeType.split('/')[1]) || 'png';
                filesToSend.push(new File([blob], 'pasted-image.' + sub, { type: pendingImage.mimeType || 'image/png' }));
            } catch (err) {
                console.warn('goclaw chat: could not convert pasted image for multipart', err);
            }
        }

        appendMessage('user', message, imageUrl);
        
        $input.val('');
        $input.css('height', 'auto');
        pendingImage = null;
        pendingFiles = [];
        $('#image-preview').hide();
        $('#preview-img').attr('src', '');
        renderPendingFilesList();
        $sendBtn.prop('disabled', true);
        updateComposerReserve();
        
        if (filesToSend.length > 0) {
            var fd = new FormData();
            fd.append('message', message || '');
            filesToSend.forEach(function(f) {
                fd.append('files', f, f.name || 'upload.bin');
            });
            $.ajax({
                url: '/api/send/multipart',
                method: 'POST',
                data: fd,
                processData: false,
                contentType: false,
                success: function(data) {
                    console.log('Message sent (multipart):', data);
                },
                error: function(xhr) {
                    appendMessage('error', 'Failed to send message: ' + xhr.responseText);
                },
                complete: function() {
                    $sendBtn.prop('disabled', false);
                    $input.focus();
                }
            });
            return;
        }
        
        var payload = { message: message || '' };
        $.ajax({
            url: '/api/send',
            method: 'POST',
            contentType: 'application/json',
            data: JSON.stringify(payload),
            success: function(data) {
                console.log('Message sent:', data);
            },
            error: function(xhr) {
                appendMessage('error', 'Failed to send message: ' + xhr.responseText);
            },
            complete: function() {
                $sendBtn.prop('disabled', false);
                $input.focus();
            }
        });
    });

    // File attachments (not in supervision — no UI)
    if (!isSupervising) {
        $('#file-attach-input').on('change', function() {
            var inp = this;
            if (inp.files && inp.files.length) {
                for (var fi = 0; fi < inp.files.length; fi++) {
                    pendingFiles.push(inp.files[fi]);
                }
            }
            inp.value = '';
            renderPendingFilesList();
            updateComposerReserve();
        });
        $('#attach-file-btn').on('click', function() {
            $('#file-attach-input').trigger('click');
        });
        $('.chat-root').on('dragover', function(ev) {
            ev.preventDefault();
            if (ev.originalEvent && ev.originalEvent.dataTransfer) {
                ev.originalEvent.dataTransfer.dropEffect = 'copy';
            }
        });
        $('.chat-root').on('drop', function(ev) {
            ev.preventDefault();
            var dt = ev.originalEvent && ev.originalEvent.dataTransfer;
            if (!dt || !dt.files || !dt.files.length) return;
            for (var di = 0; di < dt.files.length; di++) {
                pendingFiles.push(dt.files[di]);
            }
            renderPendingFilesList();
            updateComposerReserve();
        });
    }
    
    // Clear history button
    $('#clear-history').on('click', function() {
        if (confirm('Clear chat history?')) {
            clearHistory();
        }
    });

    $('#open-transcript-btn').on('click', function() {
        var url = '/chat/transcript';
        if (isSupervising && superviseSession) {
            url += '?supervise=' + encodeURIComponent(superviseSession);
        }
        window.open(url, '_blank', 'noopener');
    });

    // Prevent in-tab navigation from chat-rendered links.
    $(document).on('click', '#chat-messages a[href]', function(e) {
        var href = $(this).attr('href');
        if (!href) return;
        e.preventDefault();
        window.open(href, '_blank', 'noopener,noreferrer');
    });

    $('#stream-mode-menu').on('click', '[data-stream-mode]', function(e) {
        e.preventDefault();
        var mode = String($(this).data('stream-mode') || '').toLowerCase();
        setStreamMarkdownMode(mode);
    });

    // Command palette (GET /api/commands)
    var commandsPaletteCache = null;
    function hideCommandsModal() {
        var el = document.getElementById('commands-modal');
        if (!el || typeof bootstrap === 'undefined') return;
        var inst = bootstrap.Modal.getInstance(el);
        if (inst) inst.hide();
    }
    function fillCommandsPalette(items) {
        var $list = $('#commands-list');
        $list.empty();
        if (!items || !items.length) {
            $list.append($('<div class="list-group-item text-muted">No commands</div>'));
            return;
        }
        items.forEach(function(c) {
            var title = c.name + (c.usage ? ' ' + c.usage : '');
            var sub = c.description || '';
            if (c.aliases && c.aliases.length) {
                sub += (sub ? ' — ' : '') + 'Aliases: ' + c.aliases.join(', ');
            }
            var $btn = $('<button type="button" class="list-group-item list-group-item-action text-start"></button>');
            $btn.append($('<div class="fw-semibold"></div>').text(title));
            if (sub) $btn.append($('<div class="text-muted"></div>').text(sub));
            $btn.on('click', function() {
                $input.val(c.name + ' ');
                $input.trigger('input');
                updateComposerReserve();
                hideCommandsModal();
                $input.focus();
            });
            $list.append($btn);
        });
    }
    $('#commands-modal').on('show.bs.modal', function() {
        var $list = $('#commands-list');
        if (commandsPaletteCache) {
            fillCommandsPalette(commandsPaletteCache);
            return;
        }
        $list.html('<div class="list-group-item text-muted">Loading…</div>');
        $.ajax({
            url: '/api/commands',
            method: 'GET',
            dataType: 'json'
        }).done(function(data) {
            commandsPaletteCache = data;
            fillCommandsPalette(data);
        }).fail(function() {
            commandsPaletteCache = null;
            $list.html('<div class="list-group-item text-danger">Failed to load commands.</div>');
        });
    });
    
    // Paste handler for images
    $(document).on('paste', function(e) {
        var clipboardData = e.originalEvent.clipboardData;
        if (!clipboardData || !clipboardData.items) return;
        
        for (var i = 0; i < clipboardData.items.length; i++) {
            var item = clipboardData.items[i];
            if (item.type.indexOf('image') !== -1) {
                e.preventDefault();
                var file = item.getAsFile();
                if (file) {
                    handleImageFile(file);
                }
                return;
            }
        }
    });
    
    // Handle dropped/pasted image file
    function handleImageFile(file) {
        var reader = new FileReader();
        reader.onload = function(e) {
            var dataUrl = e.target.result;
            // Extract base64 and mime type
            var matches = dataUrl.match(/^data:(.+);base64,(.+)$/);
            if (matches) {
                pendingImage = {
                    data: matches[2],
                    mimeType: matches[1],
                    dataUrl: dataUrl
                };
                // Show preview
                $('#preview-img').attr('src', dataUrl);
                $('#image-preview').show();
                updateComposerReserve();
                $input.focus();
            }
        };
        reader.readAsDataURL(file);
    }
    
    // Remove pending image
    $('#remove-image').on('click', function() {
        pendingImage = null;
        $('#image-preview').hide();
        $('#preview-img').attr('src', '');
        updateComposerReserve();
        $input.focus();
    });

    // Media modal for enlarged view
    var $modal = $('#media-modal');
    var $modalContent = $('#media-modal-content');
    
    // Click on image or video to enlarge
    $(document).on('click', '.chat-media-image, .chat-media-video', function(e) {
        e.preventDefault();
        var $el = $(this);
        var src = $el.attr('src');
        
        if ($el.hasClass('chat-media-image')) {
            $modalContent.html('<img src="' + src + '" alt="Enlarged view">');
        } else if ($el.hasClass('chat-media-video')) {
            $modalContent.html('<video src="' + src + '" controls autoplay></video>');
        }
        
        $modal.addClass('active');
    });
    
    // Close modal on click anywhere
    $modal.on('click', function() {
        $modal.removeClass('active');
        // Stop video if playing
        $modalContent.find('video').each(function() {
            this.pause();
        });
        $modalContent.empty();
    });
    
    // Prevent closing when clicking on the media itself
    $modalContent.on('click', function(e) {
        e.stopPropagation();
    });
    
    // Close modal on Escape key
    $(document).on('keydown', function(e) {
        if (e.key === 'Escape' && $modal.hasClass('active')) {
            $modal.removeClass('active');
            $modalContent.find('video').each(function() {
                this.pause();
            });
            $modalContent.empty();
        }
    });

    // Update thinking button state
    function updateThinkingButton(enabled) {
        var $btn = $('#thinking-toggle');
        $btn.toggleClass('active', enabled);
        $btn.attr('title', 'Thinking ' + (enabled ? 'ON' : 'OFF') + ' (click to toggle)');
    }
    
    // Thinking toggle button click - send /thinking command
    $('#thinking-toggle').on('click', function() {
        // Send /thinking toggle command
        $.ajax({
            url: '/api/send',
            method: 'POST',
            contentType: 'application/json',
            data: JSON.stringify({ message: '/thinking toggle' })
        });
    });
    
    // Show thinking toggle button on chat page (not in supervision mode)
    if (!isSupervising) {
        $('#thinking-toggle').removeClass('d-none');
    }
    
    // --- Supervision mode controls ---
    
    // Update LLM status UI
    function updateLLMStatusUI() {
        var $status = $('#llm-status');
        var $btn = $('#llm-toggle');
        if (llmEnabled) {
            $status.text('LLM: Enabled').removeClass('bg-warning').addClass('bg-success');
            $btn.text('Disable LLM').removeClass('btn-success').addClass('btn-warning');
        } else {
            $status.text('LLM: Disabled').removeClass('bg-success').addClass('bg-warning');
            $btn.text('Enable LLM').removeClass('btn-warning').addClass('btn-success');
        }
    }
    
    // Debug toggle button - toggles visibility of debug-content elements
    $('#debug-toggle').on('click', function() {
        showDebug = !showDebug;
        var $card = $('.supervise-mode');
        if (showDebug) {
            $card.addClass('show-debug');
            $(this).text('Hide Debug');
        } else {
            $card.removeClass('show-debug');
            $(this).text('Show Debug');
        }
    });
    
    // LLM toggle button - enables/disables agent responses
    $('#llm-toggle').on('click', function() {
        var newState = !llmEnabled;
        $.ajax({
            url: '/api/sessions/' + encodeURIComponent(superviseSession) + '/llm',
            method: 'POST',
            contentType: 'application/json',
            data: JSON.stringify({ enabled: newState }),
            success: function(data) {
                llmEnabled = data.llmEnabled;
                updateLLMStatusUI();
            },
            error: function(xhr) {
                alert('Failed to toggle LLM: ' + xhr.responseText);
            }
        });
    });
    
    // Make tool input/output collapsible
    $(document).on('click', '.tool-call-input, .tool-call-output', function() {
        $(this).toggleClass('expanded');
    });

    $(document).on('click', '.tool-activity-controls [data-tool-filter]', function(e) {
        e.preventDefault();
        var filter = String($(this).data('tool-filter') || '').toLowerCase();
        var $panel = $(this).closest('.tool-activity-panel');
        var runId = String($panel.data('run-id') || '');
        var panel = toolPanelsByRun[runId];
        if (!panel) return;
        if (panel.runView) {
            pinRunDebug(panel.runView);
        }
        setToolPanelFilter(panel, filter);
    });

    $(document).on('toggle', '.tool-activity-item details, .thinking-content details', function() {
        var $msg = $(this).closest('.run-message');
        if (!$msg.length) return;
        var runId = String($msg.data('run-id') || '');
        var runView = getRunView(runId);
        if (!runView) return;
        pinRunDebug(runView);
    });

    // P6: load earlier — button + near-top scroll (debounced, rate-limited)
    $('#chat-load-earlier').on('click', function() {
        prependHistoryBatch(CHAT_LOAD_EARLIER_BATCH);
    });
    $messages.on('scroll', function() {
        if (historyWindowStart <= 0) return;
        if (!$messages.length) return;
        if ($messages[0].scrollTop > 200) return;
        if (Date.now() - lastAutoPrependAt < CHAT_LOAD_EARLIER_MIN_GAP_MS) return;
        if (loadEarlierScrollTimer) return;
        loadEarlierScrollTimer = setTimeout(function() {
            loadEarlierScrollTimer = null;
            if (historyWindowStart > 0 && $messages.length && $messages[0].scrollTop <= 200) {
                prependHistoryBatch(CHAT_LOAD_EARLIER_BATCH);
                lastAutoPrependAt = Date.now();
            }
        }, CHAT_LOAD_EARLIER_SCROLL_DEBOUNCE_MS);
    });

    // Load history and start connection
    updateStreamModeUI();
    loadHistory();
    connect();
    function handleWindowResize() {
        updateComposerReserve();
        updateInitialHistoryBottomAnchorLayout();
    }
    updateComposerReserve();
    $(window).on('resize', handleWindowResize);
    $input.focus();
    });
})();
