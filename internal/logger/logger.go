// Package logger provides a conditional debug logger controlled by config.Debug.
package logger

import "log"

var debugEnabled bool

// Init sets whether debug logging is active. Call once at startup.
func Init(debug bool) {
	debugEnabled = debug
	if debug {
		log.Println("[logger] debug logging enabled")
	}
}

// Debug logs a message only when debug mode is on.
func Debug(format string, args ...interface{}) {
	if debugEnabled {
		log.Printf(format, args...)
	}
}

// IsEnabled returns true if debug logging is on.
func IsEnabled() bool { return debugEnabled }
