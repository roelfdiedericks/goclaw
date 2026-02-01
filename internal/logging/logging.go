// Package logging provides global logging functions for GoClaw.
// Use dot import to access L_info, L_error, etc. directly.
package logging

import (
	"fmt"
	"os"
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
			CallerOffset:    1, // Skip one frame (our L_* wrapper)
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

// L_trace logs at trace level (mapped to debug)
func L_trace(format string, args ...interface{}) {
	ensureInit()
	logger.Debug(fmt.Sprintf(format, args...))
}

// L_debug logs at debug level
func L_debug(format string, args ...interface{}) {
	ensureInit()
	logger.Debug(fmt.Sprintf(format, args...))
}

// L_info logs at info level
func L_info(format string, args ...interface{}) {
	ensureInit()
	logger.Info(fmt.Sprintf(format, args...))
}

// L_warn logs at warn level
func L_warn(format string, args ...interface{}) {
	ensureInit()
	logger.Warn(fmt.Sprintf(format, args...))
}

// L_error logs at error level
func L_error(format string, args ...interface{}) {
	ensureInit()
	logger.Error(fmt.Sprintf(format, args...))
}

// L_fatal logs at fatal level and exits
func L_fatal(format string, args ...interface{}) {
	ensureInit()
	logger.Fatal(fmt.Sprintf(format, args...))
}

// WithFields returns a logger with additional context fields
// Usage: WithFields("user", 123, "action", "login").Info("logged in")
func WithFields(keyvals ...interface{}) *log.Logger {
	ensureInit()
	return logger.With(keyvals...)
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

// L_object prints an object for debugging (uses %+v)
func L_object(label string, obj interface{}) {
	ensureInit()
	logger.Debug(label, "value", fmt.Sprintf("%+v", obj))
}

// L_elapsed logs with elapsed time since start
func L_elapsed(start time.Time, format string, args ...interface{}) {
	ensureInit()
	elapsed := time.Since(start)
	logger.Info(fmt.Sprintf(format, args...), "elapsed", elapsed.String())
}
