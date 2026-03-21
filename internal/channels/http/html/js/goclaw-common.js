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

/**
 * Map logical role to message CSS class names.
 */
function messageRoleClass(role) {
    var roleClass = 'assistant';
    if (role === 'user') roleClass = 'user';
    else if (role === 'error') roleClass = 'error';
    else if (role === 'system') roleClass = 'system';
    else if (role === 'guidance') roleClass = 'supervision guidance';
    else if (role === 'ghostwrite') roleClass = 'supervision ghostwrite';
    else if (typeof role === 'string' && role.indexOf('mirror-') === 0) roleClass = 'mirror ' + role;
    return roleClass;
}

/**
 * Build one `.message` element used by chat + transcript pages.
 * options:
 * - role, content, imageUrl, metadata { supervisor, interventionType, source }
 * - useJQuery: return jQuery object when available
 */
function buildMessageElement(options) {
    options = options || {};
    var role = options.role || 'assistant';
    var content = options.content || '';
    var imageUrl = options.imageUrl || '';
    var metadata = options.metadata || {};
    var useJQuery = !!options.useJQuery;

    var msg = document.createElement('div');
    msg.className = 'message ' + messageRoleClass(role);
    var bubble = document.createElement('div');
    bubble.className = 'bubble';
    msg.appendChild(bubble);

    msg.dataset.rawContent = content;
    if (metadata.supervisor) msg.dataset.supervisor = metadata.supervisor;
    if (metadata.interventionType) msg.dataset.interventionType = metadata.interventionType;
    if (metadata.source) msg.dataset.source = metadata.source;

    if (imageUrl) {
        var img = document.createElement('img');
        img.src = imageUrl;
        img.className = 'inline-image';
        img.style.maxWidth = '300px';
        img.style.maxHeight = '200px';
        img.style.borderRadius = '0.5rem';
        img.style.display = 'block';
        img.style.marginBottom = '0.5rem';
        bubble.appendChild(img);
    }

    if (content) {
        var displayContent = content;
        if ((role === 'guidance' || role === 'ghostwrite') && metadata.supervisor) {
            var label = role === 'guidance' ? 'Guidance' : 'Ghostwrite';
            var prefix = '[' + label + ':' + metadata.supervisor + '] ';
            if (content.indexOf('[' + label + ':') !== 0) {
                displayContent = prefix + content;
            }
        }
        if (role === 'error') {
            var span = document.createElement('span');
            span.textContent = displayContent;
            bubble.appendChild(span);
        } else {
            bubble.innerHTML += renderMarkdown(displayContent);
        }
    }

    if (useJQuery && typeof window !== 'undefined' && window.jQuery) {
        return window.jQuery(msg);
    }
    return msg;
}

// Export for module systems (optional)
if (typeof module !== 'undefined' && module.exports) {
    module.exports = {
        escapeHtml,
        renderMarkdown,
        renderMediaRefs,
        unescapeMediaPath,
        formatTimestamp,
        formatDuration,
        messageRoleClass,
        buildMessageElement
    };
}
