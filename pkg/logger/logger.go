// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package logger

import (
	"os"
	"sync"
	"time"

	"github.com/dapr/dapr/pkg/version"
	"github.com/dapr/dapr/utils"
	"github.com/sirupsen/logrus"
)

const (
	// LogTypeLog is normal log type
	LogTypeLog = "log"
	// LogTypeRequest is Request log type
	LogTypeRequest = "request"

	logFieldTimeStamp = "time"
	logFieldLevel     = "level"
	logFieldType      = "log_type"
	logFieldScope     = "scope"
	logFieldMessage   = "message"
	logFieldInstance  = "instance"
	logFieldDaprVer   = "dapr_ver"
	logFieldDaprID    = "dapr_id"
	logFieldRequestID = "request_id"
)

// Level is Dapr Logger Level type
type Level int

const (
	// DebugLevel has verbose message
	DebugLevel Level = iota
	// InfoLevel is default log level
	InfoLevel
	// WarnLevel is for logging messages about possible issues
	WarnLevel
	// ErrorLevel is for logging errors
	ErrorLevel
	// FatalLevel is for logging fatal messages. The system shutsdown after logging the message.
	FatalLevel
)

var daprLoggingLevelToLogrusLevel = map[Level]logrus.Level{
	DebugLevel: logrus.DebugLevel,
	InfoLevel:  logrus.InfoLevel,
	WarnLevel:  logrus.WarnLevel,
	ErrorLevel: logrus.ErrorLevel,
	FatalLevel: logrus.FatalLevel,
}

type Logger interface {
	// SetDaprID sets dapr_id field in log. Default value is empty string
	SetDaprID(id string)
	// SetScope sets scope field in log. Default value is Logger name
	SetScope(scope string)
	// SetOutputLevel sets output level
	SetOutputLevel(outputLevel Level)

	// EnableJSONOutput enables JSON formatted output log
	EnableJSONOutput(enabled bool)

	// WithLogType changes the default log_type field in log. Default value is LogTypeLog
	WithLogType(logType string) Logger

	// Info logs a message at level Info.
	Info(args ...interface{})
	// Infof logs a message at level Info.
	Infof(format string, args ...interface{})
	// Debug logs a message at level Debug.
	Debug(args ...interface{})
	// Debugf logs a message at level Debug.
	Debugf(format string, args ...interface{})
	// Warn logs a message at level Warn.
	Warn(args ...interface{})
	// Warnf logs a message at level Warn.
	Warnf(format string, args ...interface{})
	// Error logs a message at level Error.
	Error(args ...interface{})
	// Errorf logs a message at level Error.
	Errorf(format string, args ...interface{})
	// Fatal logs a message at level Fatal then the process will exit with status set to 1.
	Fatal(args ...interface{})
	// Fatalf logs a message at level Fatal then the process will exit with status set to 1.
	Fatalf(format string, args ...interface{})
}

type daprLogger struct {
	name              string
	jsonFormatEnabled bool
	logger            *logrus.Entry
}

var loggers = map[string]Logger{}
var loggersLock = sync.RWMutex{}

// NewLogger creates new Logger instance.
func NewLogger(name string) Logger {
	loggersLock.Lock()
	defer loggersLock.Unlock()

	logger, ok := loggers[name]

	if !ok {
		newLogger := logrus.New()
		dl := &daprLogger{
			name:              name,
			jsonFormatEnabled: defaultJSONOutput,
			logger:            newDefaultLogger(newLogger, name),
		}
		dl.EnableJSONOutput(defaultJSONOutput)
		loggers[name] = dl
		logger = loggers[name]
	}

	return logger
}

func newDefaultLogger(logger *logrus.Logger, name string) *logrus.Entry {
	return logger.WithFields(logrus.Fields{
		logFieldScope: name,
		logFieldType:  LogTypeLog,
	})
}

func getLoggers() map[string]Logger {
	loggersLock.RLock()
	defer loggersLock.RUnlock()

	l := map[string]Logger{}
	for k, v := range loggers {
		l[k] = v
	}

	return l
}

func (l *daprLogger) SetDaprID(id string) {
	if l.jsonFormatEnabled {
		l.logger.Data[logFieldDaprID] = id
	}
}

func (l *daprLogger) SetScope(scope string) {
	l.logger.Data[logFieldScope] = scope
}

func (l *daprLogger) SetOutputLevel(outputLevel Level) {
	lvl, ok := daprLoggingLevelToLogrusLevel[outputLevel]
	if !ok {
		lvl = daprLoggingLevelToLogrusLevel[defaultOutputLevel]
	}
	l.logger.Logger.SetLevel(lvl)
}

func (l *daprLogger) EnableJSONOutput(enabled bool) {
	var formatter logrus.Formatter

	l.jsonFormatEnabled = enabled
	fieldMap := logrus.FieldMap{
		logrus.FieldKeyLevel: logFieldLevel,
		logrus.FieldKeyMsg:   logFieldMessage,
	}

	if enabled {
		hostname, _ := os.Hostname()
		l.logger.Data = logrus.Fields{
			logFieldScope:    l.logger.Data[logFieldScope],
			logFieldInstance: hostname,
			logFieldType:     LogTypeLog,
			logFieldDaprVer:  version.Version(),
		}
		formatter = &logrus.JSONFormatter{
			DisableTimestamp: true,
			FieldMap:         fieldMap,
		}
	} else {
		l.logger.Data = logrus.Fields{
			logFieldScope: l.logger.Data[logFieldScope],
			logFieldType:  LogTypeLog,
		}
		formatter = &logrus.TextFormatter{
			DisableTimestamp: true,
			FieldMap:         fieldMap,
		}
	}

	l.logger.Logger.SetFormatter(formatter)
}

func (l *daprLogger) WithLogType(logType string) Logger {
	return &daprLogger{
		name:   l.name,
		logger: l.logger.WithField(logFieldType, logType),
	}
}

// Info logs a message at level Info.
func (l *daprLogger) Info(args ...interface{}) {
	if l.jsonFormatEnabled {
		l.logger.Data[logFieldTimeStamp] = utils.ToISO8601DateTimeString(time.Now())
	}
	l.logger.Info(args...)
}

// Infof logs a message at level Info.
func (l *daprLogger) Infof(format string, args ...interface{}) {
	if l.jsonFormatEnabled {
		l.logger.Data[logFieldTimeStamp] = utils.ToISO8601DateTimeString(time.Now())
	}
	l.logger.Infof(format, args...)
}

// Debug logs a message at level Debug.
func (l *daprLogger) Debug(args ...interface{}) {
	if l.jsonFormatEnabled {
		l.logger.Data[logFieldTimeStamp] = utils.ToISO8601DateTimeString(time.Now())
	}
	l.logger.Debug(args...)
}

// Debugf logs a message at level Debug.
func (l *daprLogger) Debugf(format string, args ...interface{}) {
	if l.jsonFormatEnabled {
		l.logger.Data[logFieldTimeStamp] = utils.ToISO8601DateTimeString(time.Now())
	}
	l.logger.Debugf(format, args...)
}

// Warn logs a message at level Warn.
func (l *daprLogger) Warn(args ...interface{}) {
	if l.jsonFormatEnabled {
		l.logger.Data[logFieldTimeStamp] = utils.ToISO8601DateTimeString(time.Now())
	}
	l.logger.Warn(args...)
}

// Warnf logs a message at level Warn.
func (l *daprLogger) Warnf(format string, args ...interface{}) {
	if l.jsonFormatEnabled {
		l.logger.Data[logFieldTimeStamp] = utils.ToISO8601DateTimeString(time.Now())
	}
	l.logger.Warnf(format, args...)
}

// Error logs a message at level Error.
func (l *daprLogger) Error(args ...interface{}) {
	if l.jsonFormatEnabled {
		l.logger.Data[logFieldTimeStamp] = utils.ToISO8601DateTimeString(time.Now())
	}
	l.logger.Error(args...)
}

// Errorf logs a message at level Error.
func (l *daprLogger) Errorf(format string, args ...interface{}) {
	if l.jsonFormatEnabled {
		l.logger.Data[logFieldTimeStamp] = utils.ToISO8601DateTimeString(time.Now())
	}
	l.logger.Errorf(format, args...)
}

// Fatal logs a message at level Fatal then the process will exit with status set to 1.
func (l *daprLogger) Fatal(args ...interface{}) {
	if l.jsonFormatEnabled {
		l.logger.Data[logFieldTimeStamp] = utils.ToISO8601DateTimeString(time.Now())
	}
	l.logger.Fatal(args...)
}

// Fatalf logs a message at level Fatal then the process will exit with status set to 1.
func (l *daprLogger) Fatalf(format string, args ...interface{}) {
	if l.jsonFormatEnabled {
		l.logger.Data[logFieldTimeStamp] = utils.ToISO8601DateTimeString(time.Now())
	}
	l.logger.Fatalf(format, args...)
}
