// Package logging provides global logging functions for GoClaw.
// Use dot import to access L_info, L_error, etc. directly.
package logging

import (
	"fmt"
	"os"
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

	// Global shutdown flag - checked by components before operations
	shuttingDown int32
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

		// Map our levels to charmbracelet levels
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

// L_trace logs at trace level (mapped to debug)
func L_trace(msg string, args ...interface{}) {
	logMsg(log.DebugLevel, msg, args...)
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
