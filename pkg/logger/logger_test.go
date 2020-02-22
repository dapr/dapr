package logger

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"

	"github.com/dapr/dapr/pkg/version"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

const fakeLoggerName = "fakeLogger"

func clearLoggers() {
	globalLoggers = map[string]Logger{}
}

func getTestLogger(buf io.Writer) *daprLogger {
	fakeLogrusLogger := logrus.New()
	fakeLogrusLogger.SetOutput(buf)
	l := &daprLogger{
		name:              fakeLoggerName,
		jsonFormatEnabled: false,
		logger:            defaultLogger(fakeLogrusLogger, fakeLoggerName),
	}
	l.EnableJSONOutput(false)

	return l
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

func TestEnableJSON(t *testing.T) {
	t.Run("enable JSON format", func(t *testing.T) {
		var buf bytes.Buffer
		testLogger := getTestLogger(&buf)
		assert.False(t, testLogger.jsonFormatEnabled)
		assert.Equal(t, "fakeLogger", testLogger.logger.Data[logFieldScope])
		assert.Equal(t, LogTypeLog, testLogger.logger.Data[logFieldType])
		assert.Nil(t, testLogger.logger.Data[logFieldInstance])
		assert.Nil(t, testLogger.logger.Data[logFieldDaprVer])

		expectedHost, _ := os.Hostname()
		testLogger.EnableJSONOutput(true)
		assert.True(t, testLogger.jsonFormatEnabled)
		assert.Equal(t, "fakeLogger", testLogger.logger.Data[logFieldScope])
		assert.Equal(t, expectedHost, testLogger.logger.Data[logFieldInstance])
		assert.Equal(t, version.Version(), testLogger.logger.Data[logFieldDaprVer])
	})

	t.Run("disable JSON format", func(t *testing.T) {
		var buf bytes.Buffer
		testLogger := getTestLogger(&buf)

		expectedHost, _ := os.Hostname()
		testLogger.EnableJSONOutput(true)
		assert.True(t, testLogger.jsonFormatEnabled)
		assert.Equal(t, "fakeLogger", testLogger.logger.Data[logFieldScope])
		assert.Equal(t, expectedHost, testLogger.logger.Data[logFieldInstance])
		assert.Equal(t, version.Version(), testLogger.logger.Data[logFieldDaprVer])

		testLogger.EnableJSONOutput(false)
		assert.False(t, testLogger.jsonFormatEnabled)
		assert.Equal(t, "fakeLogger", testLogger.logger.Data[logFieldScope])
		assert.Equal(t, LogTypeLog, testLogger.logger.Data[logFieldType])
		assert.Nil(t, testLogger.logger.Data[logFieldInstance])
		assert.Nil(t, testLogger.logger.Data[logFieldDaprVer])
	})
}

func TestJSONLoggerFields(t *testing.T) {
	tests := []struct {
		name        string
		outputLevel LogLevel
		level       string
		daprID      string
		message     string
		instance    string
		fn          func(*daprLogger, string)
	}{
		{
			"info()",
			InfoLevel,
			"info",
			"dapr_app",
			"King Dapr",
			"dapr-pod",
			func(l *daprLogger, msg string) {
				l.Info(msg)
			},
		},
		{
			"infof()",
			InfoLevel,
			"info",
			"dapr_app",
			"King Dapr",
			"dapr-pod",
			func(l *daprLogger, msg string) {
				l.Infof("%s", msg)
			},
		},
		{
			"debug()",
			DebugLevel,
			"debug",
			"dapr_app",
			"King Dapr",
			"dapr-pod",
			func(l *daprLogger, msg string) {
				l.Debug(msg)
			},
		},
		{
			"debugf()",
			DebugLevel,
			"debug",
			"dapr_app",
			"King Dapr",
			"dapr-pod",
			func(l *daprLogger, msg string) {
				l.Debugf("%s", msg)
			},
		},
		{
			"error()",
			InfoLevel,
			"error",
			"dapr_app",
			"King Dapr",
			"dapr-pod",
			func(l *daprLogger, msg string) {
				l.Error(msg)
			},
		},
		{
			"errorf()",
			InfoLevel,
			"error",
			"dapr_app",
			"King Dapr",
			"dapr-pod",
			func(l *daprLogger, msg string) {
				l.Errorf("%s", msg)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			testLogger := getTestLogger(&buf)
			testLogger.EnableJSONOutput(true)
			testLogger.SetDaprID(tt.daprID)
			testLogger.SetOutputLevel(tt.outputLevel)
			testLogger.logger.Data[logFieldInstance] = tt.instance

			tt.fn(testLogger, tt.message)

			b, _ := buf.ReadBytes('\n')
			var o map[string]interface{}
			json.Unmarshal(b, &o)

			// assert
			assert.Equal(t, tt.daprID, o[logFieldDaprID])
			assert.Equal(t, tt.instance, o[logFieldInstance])
			assert.Equal(t, tt.level, o[logFieldLevel])
			assert.Equal(t, LogTypeLog, o[logFieldType])
			assert.Equal(t, fakeLoggerName, o[logFieldScope])
			assert.Equal(t, tt.message, o[logFieldMessage])
			_, err := time.Parse(time.RFC3339, o[logFieldTimeStamp].(string))
			assert.NoError(t, err)
		})
	}
}
