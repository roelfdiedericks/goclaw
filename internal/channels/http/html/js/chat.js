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
    function scrollChatToBottom() {
        requestAnimationFrame(function() {
            window.scrollTo(0, document.documentElement.scrollHeight);
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
    var historyWindowStart = 0;
    var historyWindowEnd = -1;
    var deferHistoryRebuildAfterStream = false;
    var loadEarlierScrollTimer = null;
    var lastAutoPrependAt = 0;
    var loadEarlierInProgress = false;

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
            canonicalLen: canonicalHistory.length,
            maxStored: MAX_MESSAGES,
            streaming: streamingActive()
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
        var ci = $firstKeep.attr('data-canonical-index');
        if (ci !== undefined && ci !== '') {
            var parsedIdx = parseInt(ci, 10);
            if (!isNaN(parsedIdx)) {
                historyWindowStart = parsedIdx;
            } else {
                historyWindowStart += excess;
            }
        } else {
            historyWindowStart += excess;
        }
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
    var activeToolCalls = {}; // Track active tool calls by ID

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
        if (!$currentBubble || showThinking) return;
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
        if (streamMarkdownMode !== 'debounced' || showThinking || !$currentBubble) return;
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

    function rebuildHistoryDomFromCanonicalWindow() {
        if (streamingActive()) return;
        var $typing = $('#typing-indicator').detach();
        var $tools = $('#chat-history-tools').detach();
        $messages.empty();
        if ($tools.length) {
            $messages.append($tools);
        }
        var mounted = 0;
        for (var i = historyWindowStart; i <= historyWindowEnd && i >= 0 && i < canonicalHistory.length; i++) {
            var msg = canonicalHistory[i];
            var $m = buildMessageElement(msg.role, msg.content, null, {
                supervisor: msg.supervisor,
                interventionType: msg.interventionType,
                source: msg.source
            });
            $m.attr('data-canonical-index', String(i));
            $messages.append($m);
            mounted++;
        }
        if ($typing.length) {
            $messages.append($typing);
        }
        updateLoadEarlierVisibility();
        logDomCap('rebuild-from-canonical', { mountedRows: mounted });
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
        historyWindowEnd -= n;
        if (historyWindowEnd < historyWindowStart - 1) {
            historyWindowEnd = historyWindowStart - 1;
        }
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
                logDomCap('prepend-start', { batch: batch, windowBefore: winBefore });
                var oldH = document.documentElement.scrollHeight;
                var oldY = window.scrollY;

                var $tools = $('#chat-history-tools');
                var $after = $tools;
                for (var idx = historyWindowStart - batch; idx < historyWindowStart; idx++) {
                    var msg = canonicalHistory[idx];
                    var $m = buildMessageElement(msg.role, msg.content, null, {
                        supervisor: msg.supervisor,
                        interventionType: msg.interventionType,
                        source: msg.source
                    });
                    $m.attr('data-canonical-index', String(idx));
                    $after.after($m);
                    $after = $m;
                }
                historyWindowStart -= batch;

                var $msgs = $messages.children('.message');
                var over = $msgs.length - chatDomCap;
                if (over > 0) {
                    logDomCap('prepend-cap-overflow', { domRowsBeforeCap: $msgs.length, dropFromBottom: over });
                    removeBottomMessageBlocks(over);
                }

                var newH = document.documentElement.scrollHeight;
                window.scrollTo(0, oldY + (newH - oldH));
                updateLoadEarlierVisibility();
                logDomCap('prepend-done', {
                    windowAfter: historyWindowStart + '..' + historyWindowEnd,
                    domMessageRows: $messages.children('.message').length
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

    // Load chat history — hydrate canonical; mount tail window [historyWindowStart..end] (P6)
    function loadHistory() {
        try {
            var parsed = JSON.parse(localStorage.getItem(STORAGE_KEY) || '[]');
            canonicalHistory = Array.isArray(parsed) ? parsed : [];
            historyWindowEnd = canonicalHistory.length - 1;
            historyWindowStart = Math.max(0, canonicalHistory.length - chatDomCap);

            for (var hi = historyWindowStart; hi < canonicalHistory.length; hi++) {
                var msg = canonicalHistory[hi];
                var $m = buildMessageElement(msg.role, msg.content, null, {
                    supervisor: msg.supervisor,
                    interventionType: msg.interventionType,
                    source: msg.source
                });
                $m.attr('data-canonical-index', String(hi));
                $messages.append($m);
            }
            updateLoadEarlierVisibility();
            scrollChatToBottom();
            logDomCap('load-history', { mountedFromIndex: historyWindowStart });
        } catch (err) {
            console.error('Failed to load chat history:', err);
            canonicalHistory = [];
            historyWindowStart = 0;
            historyWindowEnd = -1;
        }
    }

    // Clear chat history
    function clearHistory() {
        localStorage.removeItem(STORAGE_KEY);
        canonicalHistory = [];
        historyWindowStart = 0;
        historyWindowEnd = -1;
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
            currentRunId = data.RunID;
            currentRawText = ''; // Reset raw text accumulator
            cancelStreamMarkdownDebounce();

            if (showThinking) {
                // BUFFER MODE: Don't create bubble yet - tools will appear first
                // Bubble will be created in 'done' handler after tools
                $currentBubble = null;
                showTypingIndicator();
            } else {
                // NORMAL MODE: Create bubble for streaming
                var $msg = $('<div class="message assistant"><div class="bubble"></div></div>');
                $currentBubble = $msg.find('.bubble');
                $messages.append($msg);
                trimChatDomIfNeeded();
                showTypingIndicator();
            }
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
            
            if (showThinking) {
                // BUFFER MODE: Just accumulate, don't display yet
                // Response will appear after tools in 'done' handler
            } else {
                // NORMAL MODE: Stream to bubble (plain text vs debounced markdown)
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
            // Use finalText from server (has media refs enriched) instead of accumulated deltas
            var finalText = data.finalText || currentRawText;
            
            if (showThinking && !$currentBubble && finalText) {
                // BUFFER MODE: Create bubble NOW (after tools have been shown)
                var $msg = $('<div class="message assistant"><div class="bubble"></div></div>');
                $currentBubble = $msg.find('.bubble');
                $messages.append($msg);
                trimChatDomIfNeeded();
            }

            if (finalText && String(finalText).trim().length > 0) {
                announceToScreenReader('Assistant reply complete.');
                var rebuiltDone = appendCanonicalMessage({ role: 'assistant', content: finalText });
                if (!rebuiltDone && $currentBubble) {
                    $currentBubble.html(renderMarkdown(finalText));
                    $currentBubble.closest('.message').attr('data-canonical-index', String(canonicalHistory.length - 1));
                    $currentBubble.closest('.message').data('raw-content', finalText);
                }
            }
            currentRunId = null;
            currentRawText = '';
            $currentBubble = null;
            if (deferHistoryRebuildAfterStream) {
                deferHistoryRebuildAfterStream = false;
                historyWindowEnd = canonicalHistory.length - 1;
                historyWindowStart = Math.max(0, canonicalHistory.length - chatDomCap);
                rebuildHistoryDomFromCanonicalWindow();
            } else {
                trimChatDomIfNeeded();
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
            appendMessage('error', 'Error: ' + data.error);
            announceToScreenReader('Agent error.');
            currentRunId = null;
            $currentBubble = null;
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
            
            // In normal mode, skip if thinking disabled
            if (!isSupervising && !showThinking) return;
            
            var debugClass = isSupervising ? ' debug-content' : '';
            var $toolCall = $('<div class="tool-call' + debugClass + '" data-tool-id="' + toolId + '">' +
                '<div class="tool-call-header">' +
                    '<span class="tool-call-icon"><i class="bi bi-gear-fill"></i></span>' +
                    '<span class="tool-call-name">' + escapeHtml(data.toolName) + '</span>' +
                    '<span class="tool-call-status running"><i class="bi bi-arrow-repeat spin"></i> Running</span>' +
                '</div>' +
                '<div class="tool-call-input">' + escapeHtml(inputStr) + '</div>' +
            '</div>');
            
            $messages.append($toolCall);
            scrollChatToBottom();
            activeToolCalls[toolId] = $toolCall;
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
            var $toolCall = activeToolCalls[toolId];
            
            // In normal mode, skip if thinking disabled (and we didn't create the element)
            if (!isSupervising && !showThinking && !$toolCall) return;
            
            if ($toolCall) {
                var statusClass = data.error ? 'error' : 'completed';
                var statusIcon = data.error ? 'x-circle' : 'check-circle';
                var statusText = data.error ? 'Error' : 'Completed';
                var duration = data.durationMs ? ' (' + data.durationMs + 'ms)' : '';
                
                $toolCall.find('.tool-call-status')
                    .removeClass('running')
                    .addClass(statusClass)
                    .html('<i class="bi bi-' + statusIcon + '"></i> ' + statusText + duration);
                
                // Add result/error
                if (data.result || data.error) {
                    var output = data.error || data.result;
                    $toolCall.append('<div class="tool-call-output">' + escapeHtml(output) + '</div>');
                }
                
                delete activeToolCalls[toolId];
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
            var content = data.content || '';
            var debugClass = isSupervising ? ' debug-content' : '';
            
            // Show reasoning content in collapsible element
            var $thinking = $('<div class="thinking-content' + debugClass + '">' +
                '<details>' +
                    '<summary><i class="bi bi-lightbulb"></i> Reasoning</summary>' +
                    '<div class="thinking-text">' + escapeHtml(content) + '</div>' +
                '</details>' +
            '</div>');
            $messages.append($thinking);
            scrollChatToBottom();
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

    // P6: load earlier — button + near-top scroll (debounced, rate-limited)
    $('#chat-load-earlier').on('click', function() {
        prependHistoryBatch(CHAT_LOAD_EARLIER_BATCH);
    });
    $(window).on('scroll', function() {
        if (historyWindowStart <= 0) return;
        if (window.scrollY > 200) return;
        if (Date.now() - lastAutoPrependAt < CHAT_LOAD_EARLIER_MIN_GAP_MS) return;
        if (loadEarlierScrollTimer) return;
        loadEarlierScrollTimer = setTimeout(function() {
            loadEarlierScrollTimer = null;
            if (historyWindowStart > 0 && window.scrollY <= 200) {
                prependHistoryBatch(CHAT_LOAD_EARLIER_BATCH);
                lastAutoPrependAt = Date.now();
            }
        }, CHAT_LOAD_EARLIER_SCROLL_DEBOUNCE_MS);
    });

    // Load history and start connection
    updateStreamModeUI();
    loadHistory();
    connect();
    updateComposerReserve();
    $(window).on('resize', updateComposerReserve);
    $input.focus();
    });
})();
