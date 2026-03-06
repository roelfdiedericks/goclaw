/**
 * GoClaw Common JavaScript Library
 * Shared utilities for chat and voice pages
 */

// Configure marked.js with highlight.js
if (typeof marked !== 'undefined') {
    marked.setOptions({
        highlight: function(code, lang) {
            if (lang && typeof hljs !== 'undefined' && hljs.getLanguage(lang)) {
                try {
                    return hljs.highlight(code, { language: lang }).value;
                } catch (e) {}
            }
            return code;
        },
        breaks: true,
        gfm: true
    });
}

/**
 * Escape HTML for safe display
 */
function escapeHtml(text) {
    if (!text) return '';
    var div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

/**
 * Render markdown safely
 */
function renderMarkdown(text) {
    if (!text) return '';
    if (typeof marked === 'undefined') {
        return escapeHtml(text).replace(/\n/g, '<br>');
    }
    var html = marked.parse(text);
    if (typeof DOMPurify !== 'undefined') {
        html = DOMPurify.sanitize(html);
    }
    html = renderMediaRefs(html);
    return html;
}

/**
 * Render media references: {{media:mime:'path'}} -> HTML elements
 */
function renderMediaRefs(html) {
    // Pattern: {{media:mimetype:'escaped-path'}}
    // Also handles error mimes: {{media:error/not-found:'path'}}
    var pattern = /\{\{media:([a-z]+\/[a-z0-9.+-]+):'((?:[^'\\]|\\.)*)'\}\}/gi;
    
    return html.replace(pattern, function(match, mime, escapedPath) {
        var path = unescapeMediaPath(escapedPath);
        var url = '/api/media?path=' + encodeURIComponent(path);
        var filename = path.split('/').pop();
        
        // Handle error states
        if (mime.startsWith('error/')) {
            var errorType = mime.split('/')[1];
            return '<div class="chat-media-error"><i class="bi bi-exclamation-triangle"></i> ' +
                (errorType === 'not-found' ? 'File not found: ' : 'Invalid path: ') +
                '<code>' + escapeHtml(filename) + '</code></div>';
        }
        
        // Render based on mimetype
        if (mime.startsWith('image/')) {
            return '<img src="' + url + '" class="chat-media chat-media-image" alt="' + escapeHtml(filename) + '" loading="lazy">';
        } else if (mime.startsWith('video/')) {
            return '<video src="' + url + '" controls class="chat-media chat-media-video"></video>';
        } else if (mime.startsWith('audio/')) {
            return '<audio src="' + url + '" controls class="chat-media-audio"></audio>';
        } else {
            // Document or unknown - show download link
            return '<a href="' + url + '" download class="chat-media-link"><i class="bi bi-file-earmark-arrow-down"></i> ' +
                escapeHtml(filename) + '</a>';
        }
    });
}

/**
 * Unescape media path (reverse of Go's EscapePath)
 */
function unescapeMediaPath(s) {
    return s.replace(/\\'/g, "'").replace(/\\\\/g, "\\");
}

/**
 * Format a timestamp for display
 */
function formatTimestamp(date) {
    if (!date) date = new Date();
    return date.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit' });
}

/**
 * Format duration in seconds to MM:SS
 */
function formatDuration(seconds) {
    var mins = Math.floor(seconds / 60).toString().padStart(2, '0');
    var secs = (seconds % 60).toString().padStart(2, '0');
    return mins + ':' + secs;
}

// Export for module systems (optional)
if (typeof module !== 'undefined' && module.exports) {
    module.exports = {
        escapeHtml,
        renderMarkdown,
        renderMediaRefs,
        unescapeMediaPath,
        formatTimestamp,
        formatDuration
    };
}
