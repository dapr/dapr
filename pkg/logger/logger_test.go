package logger

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func clearLoggers() {
	globalLoggers = map[string]Logger{}
}

func TestNewLogger(t *testing.T) {
	testLoggerName := "dapr.test"

	t.Run("create new logger instance", func(t *testing.T) {
		clearLoggers()

		// act
		NewLogger(testLoggerName)
		_, ok := globalLoggers[testLoggerName]

		// assert
		assert.True(t, ok)
	})

	t.Run("return the existing logger instance", func(t *testing.T) {
		clearLoggers()

		// act
		oldLogger := NewLogger(testLoggerName)
		newLogger := NewLogger(testLoggerName)

		// assert
		assert.Equal(t, oldLogger, newLogger)
	})
}

func TestToLogLevel(t *testing.T) {
	t.Run("convert debug to DebugLevel", func(t *testing.T) {
		assert.Equal(t, DebugLevel, toLogLevel("debug"))
	})

	t.Run("convert info to InfoLevel", func(t *testing.T) {
		assert.Equal(t, InfoLevel, toLogLevel("info"))
	})

	t.Run("convert warn to WarnLevel", func(t *testing.T) {
		assert.Equal(t, WarnLevel, toLogLevel("warn"))
	})

	t.Run("convert error to ErrorLevel", func(t *testing.T) {
		assert.Equal(t, ErrorLevel, toLogLevel("error"))
	})

	t.Run("convert fatal to FatalLevel", func(t *testing.T) {
		assert.Equal(t, FatalLevel, toLogLevel("fatal"))
	})

	t.Run("undefined loglevel", func(t *testing.T) {
		assert.Equal(t, UndefinedLevel, toLogLevel("undefined"))
	})
}
