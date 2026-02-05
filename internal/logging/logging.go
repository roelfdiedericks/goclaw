// Package logging provides global logging functions for GoClaw.
// Use dot import to access L_info, L_error, etc. directly.
package logging

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
)

// Log levels
const (
	LevelFatal = iota
	LevelError
	LevelWarn
	LevelInfo
	LevelDebug
	LevelTrace
)

var (
	logger *log.Logger
	once   sync.Once

	// Current log level (used for trace filtering since charmbracelet doesn't have trace)
	currentLevel int32 = LevelInfo

	// Global shutdown flag - checked by components before operations
	shuttingDown int32

	// Log hook for TUI integration
	logHook         func(level, msg string)
	logHookLock     sync.RWMutex
	hookIsExclusive int32 // When set, don't write to stderr
)

// Config holds logging configuration
type Config struct {
	Level      int
	TimeFormat string
	ShowCaller bool
}

// DefaultConfig returns sensible defaults
func DefaultConfig() *Config {
	return &Config{
		Level:      LevelInfo,
		TimeFormat: "15:04:05",
		ShowCaller: true,
	}
}

// Init initializes the global logger. Safe to call multiple times.
func Init(cfg *Config) {
	once.Do(func() {
		if cfg == nil {
			cfg = DefaultConfig()
		}

		logger = log.NewWithOptions(os.Stderr, log.Options{
			ReportTimestamp: true,
			TimeFormat:      cfg.TimeFormat,
			ReportCaller:    cfg.ShowCaller,
			CallerOffset:    2, // Skip two frames (logMsg -> L_* -> caller)
		})

		// Store current level for trace filtering
		atomic.StoreInt32(&currentLevel, int32(cfg.Level))

		// Map our levels to charmbracelet levels
		// Note: charmbracelet doesn't have trace, so both trace and debug use DebugLevel
		// We filter trace messages manually in L_trace based on currentLevel
		switch cfg.Level {
		case LevelTrace, LevelDebug:
			logger.SetLevel(log.DebugLevel)
		case LevelInfo:
			logger.SetLevel(log.InfoLevel)
		case LevelWarn:
			logger.SetLevel(log.WarnLevel)
		case LevelError, LevelFatal:
			logger.SetLevel(log.ErrorLevel)
		}
	})
}

// ensureInit ensures logger is initialized with defaults if not already
func ensureInit() {
	if logger == nil {
		Init(nil)
	}
}

// hasFmtVerb checks if a string contains printf-style format verbs
func hasFmtVerb(s string) bool {
	for i := 0; i < len(s)-1; i++ {
		if s[i] == '%' {
			next := s[i+1]
			// Common format verbs: v, s, d, f, t, p, etc. Also %% is escape
			if next != '%' && strings.ContainsRune("vsdtfgeopqxXbcUT+#", rune(next)) {
				return true
			}
		}
	}
	return false
}

// logMsgWithPrefix logs with a custom level prefix (for trace which charmbracelet doesn't support)
func logMsgWithPrefix(prefix string, msg string, args ...interface{}) {
	ensureInit()

	var finalMsg string
	var keyvals []interface{}

	if len(args) == 0 {
		finalMsg = msg
	} else if hasFmtVerb(msg) {
		finalMsg = fmt.Sprintf(msg, args...)
	} else {
		finalMsg = msg
		keyvals = args
	}

	// Call hook if set (for TUI integration)
	logHookLock.RLock()
	hook := logHook
	logHookLock.RUnlock()
	if hook != nil {
		display := finalMsg
		for i := 0; i+1 < len(keyvals); i += 2 {
			display += fmt.Sprintf(" %v=%v", keyvals[i], keyvals[i+1])
		}
		hook(prefix, display)
	}

	// Format with custom prefix - use the underlying logger's writer
	// Format: timestamp TRAC <caller> message key=value...
	now := time.Now().Format("2006/01/02 15:04:05")
	
	// Get caller info (skip 3 frames: logMsgWithPrefix -> L_trace -> actual caller)
	_, file, line, ok := runtime.Caller(2)
	caller := ""
	if ok {
		// Extract just filename from full path
		if idx := strings.LastIndex(file, "/"); idx >= 0 {
			file = file[idx+1:]
		}
		caller = fmt.Sprintf("<%s:%d>", file, line)
	}

	// Skip stderr output if in exclusive hook mode (TUI)
	if atomic.LoadInt32(&hookIsExclusive) == 1 {
		return
	}

	// Build the log line
	var sb strings.Builder
	sb.WriteString(now)
	sb.WriteString(" ")
	sb.WriteString(prefix)
	sb.WriteString(" ")
	sb.WriteString(caller)
	sb.WriteString(" ")
	sb.WriteString(finalMsg)
	for i := 0; i+1 < len(keyvals); i += 2 {
		sb.WriteString(fmt.Sprintf(" %v=%v", keyvals[i], keyvals[i+1]))
	}
	sb.WriteString("\n")

	// Write directly to stderr
	fmt.Fprint(os.Stderr, sb.String())
}

// logMsg handles the flexible logging format:
// - logMsg(level, "message") -> simple
// - logMsg(level, "value is %d", 42) -> printf
// - logMsg(level, "loaded", "key", val, ...) -> structured
func logMsg(level log.Level, msg string, args ...interface{}) {
	ensureInit()
	
	var finalMsg string
	var keyvals []interface{}

	if len(args) == 0 {
		// Simple message
		finalMsg = msg
	} else if hasFmtVerb(msg) {
		// Printf style
		finalMsg = fmt.Sprintf(msg, args...)
	} else {
		// Structured: msg is the message, args are key-value pairs
		finalMsg = msg
		keyvals = args
	}

	// Call hook if set (for TUI integration)
	logHookLock.RLock()
	hook := logHook
	logHookLock.RUnlock()
	if hook != nil {
		levelStr := levelToString(level)
		// Format keyvals into message for display
		display := finalMsg
		for i := 0; i+1 < len(keyvals); i += 2 {
			display += fmt.Sprintf(" %v=%v", keyvals[i], keyvals[i+1])
		}
		hook(levelStr, display)
	}

	switch level {
	case log.DebugLevel:
		logger.Debug(finalMsg, keyvals...)
	case log.InfoLevel:
		logger.Info(finalMsg, keyvals...)
	case log.WarnLevel:
		logger.Warn(finalMsg, keyvals...)
	case log.ErrorLevel:
		logger.Error(finalMsg, keyvals...)
	case log.FatalLevel:
		logger.Fatal(finalMsg, keyvals...)
	}
}

// levelToString converts a log level to a string
func levelToString(level log.Level) string {
	switch level {
	case log.DebugLevel:
		return "DEBUG"
	case log.InfoLevel:
		return "INFO"
	case log.WarnLevel:
		return "WARN"
	case log.ErrorLevel:
		return "ERROR"
	case log.FatalLevel:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

// SetHook sets a function to receive all log messages.
// Pass nil to clear the hook. Used by TUI to capture logs.
// When suppressStderr is true, logs only go to the hook (not stderr).
func SetHook(hook func(level, msg string)) {
	logHookLock.Lock()
	logHook = hook
	logHookLock.Unlock()
}

// SetHookExclusive sets a hook and suppresses stderr output.
// Used by TUI to prevent log output from corrupting the display.
func SetHookExclusive(hook func(level, msg string)) {
	logHookLock.Lock()
	logHook = hook
	logHookLock.Unlock()
	
	if hook != nil {
		// Redirect logger to discard and mark exclusive mode
		atomic.StoreInt32(&hookIsExclusive, 1)
		ensureInit()
		logger.SetOutput(io.Discard)
	} else {
		// Restore stderr and clear exclusive mode
		atomic.StoreInt32(&hookIsExclusive, 0)
		ensureInit()
		logger.SetOutput(os.Stderr)
	}
}

// L_trace logs at trace level (only if trace logging is enabled)
// Trace is more verbose than debug - use for high-frequency or low-importance logs
func L_trace(msg string, args ...interface{}) {
	// Only log trace messages if level is LevelTrace
	if atomic.LoadInt32(&currentLevel) < int32(LevelTrace) {
		return
	}
	// Prefix with TRAC to distinguish from DEBU in output
	logMsgWithPrefix("TRAC", msg, args...)
}

// L_debug logs at debug level
func L_debug(msg string, args ...interface{}) {
	logMsg(log.DebugLevel, msg, args...)
}

// L_info logs at info level
func L_info(msg string, args ...interface{}) {
	logMsg(log.InfoLevel, msg, args...)
}

// L_warn logs at warn level
func L_warn(msg string, args ...interface{}) {
	logMsg(log.WarnLevel, msg, args...)
}

// L_error logs at error level
func L_error(msg string, args ...interface{}) {
	logMsg(log.ErrorLevel, msg, args...)
}

// L_fatal logs at fatal level and exits
func L_fatal(msg string, args ...interface{}) {
	logMsg(log.FatalLevel, msg, args...)
}

// SetLevel changes the log level at runtime
func SetLevel(level int) {
	ensureInit()
	
	// Store current level for trace filtering
	atomic.StoreInt32(&currentLevel, int32(level))
	
	switch level {
	case LevelTrace, LevelDebug:
		logger.SetLevel(log.DebugLevel)
	case LevelInfo:
		logger.SetLevel(log.InfoLevel)
	case LevelWarn:
		logger.SetLevel(log.WarnLevel)
	case LevelError, LevelFatal:
		logger.SetLevel(log.ErrorLevel)
	}
}

// GetLevel returns the current log level
func GetLevel() int {
	return int(atomic.LoadInt32(&currentLevel))
}

// SetShuttingDown marks the application as shutting down
func SetShuttingDown() {
	atomic.StoreInt32(&shuttingDown, 1)
	L_info("Application shutting down")
}

// IsShuttingDown returns true if application is shutting down
func IsShuttingDown() bool {
	return atomic.LoadInt32(&shuttingDown) == 1
}

// L_elapsed logs with elapsed time since start
func L_elapsed(start time.Time, msg string, args ...interface{}) {
	ensureInit()
	elapsed := time.Since(start)
	// Append elapsed to keyvals
	args = append(args, "elapsed", elapsed.String())
	logMsg(log.InfoLevel, msg, args...)
}
