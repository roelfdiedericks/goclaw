/**
 * Read-only transcript page. Renders saved localStorage history.
 * Requires goclaw-common.js for buildMessageElement/renderMarkdown/media helpers.
 */
(function () {
    'use strict';

    function getStorageKey(cfg) {
        if (cfg && cfg.isSupervising && cfg.superviseSession) {
            return 'goclaw_supervise_' + String(cfg.superviseSession);
        }
        return 'goclaw_chat_history';
    }

    function loadHistory(key) {
        try {
            var parsed = JSON.parse(localStorage.getItem(key) || '[]');
            if (!Array.isArray(parsed)) return [];
            return parsed;
        } catch (err) {
            console.warn('goclaw transcript: failed to parse history', err);
            return [];
        }
    }

    function renderTranscript(entries, $container, $empty) {
        $container.empty();
        if (!entries.length) {
            $empty.removeClass('d-none');
            return;
        }
        $empty.addClass('d-none');

        entries.forEach(function (entry) {
            if (!entry || !entry.role) return;
            var built = (typeof window.buildMessageElement === 'function')
                ? window.buildMessageElement({
                    role: entry.role,
                    content: entry.content || '',
                    imageUrl: '',
                    metadata: {
                        supervisor: entry.supervisor,
                        interventionType: entry.interventionType,
                        source: entry.source,
                    },
                    useJQuery: true,
                })
                : $('<div class="message assistant"><div class="bubble"></div></div>');
            $container.append(built);
        });
    }

    $(document).ready(function () {
        var cfg = window.__GOCLAW_TRANSCRIPT_CONFIG__ || {};
        var storageKey = getStorageKey(cfg);
        var entries = loadHistory(storageKey);
        renderTranscript(entries, $('#transcript-messages'), $('#transcript-empty'));

        // Keep transcript view open: open rendered links in a new tab.
        $(document).on('click', '#transcript-messages a[href]', function (e) {
            var href = $(this).attr('href');
            if (!href) return;
            e.preventDefault();
            window.open(href, '_blank', 'noopener,noreferrer');
        });
    });
})();
