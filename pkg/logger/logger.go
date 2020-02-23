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

	// Field names that defines Dapr log schema
	logFieldTimeStamp = "time"
	logFieldLevel     = "level"
	logFieldType      = "log_type"
	logFieldScope     = "scope"
	logFieldMessage   = "message"
	logFieldInstance  = "instance"
	logFieldDaprVer   = "dapr_ver"
	logFieldDaprID    = "dapr_id"
)

// LogLevel is Dapr Logger Level type
type LogLevel uint32

const (
	// DebugLevel has verbose message
	DebugLevel LogLevel = iota
	// InfoLevel is default log level
	InfoLevel
	// WarnLevel is for logging messages about possible issues
	WarnLevel
	// ErrorLevel is for logging errors
	ErrorLevel
	// FatalLevel is for logging fatal messages. The system shutsdown after logging the message.
	FatalLevel
)

var daprLogLevelToLogrusLevel = map[LogLevel]logrus.Level{
	DebugLevel: logrus.DebugLevel,
	InfoLevel:  logrus.InfoLevel,
	WarnLevel:  logrus.WarnLevel,
	ErrorLevel: logrus.ErrorLevel,
	FatalLevel: logrus.FatalLevel,
}

// Logger includes the logging api sets
type Logger interface {
	// EnableJSONOutput enables JSON formatted output log
	EnableJSONOutput(enabled bool)

	// SetDaprID sets dapr_id field in log. Default value is empty string
	SetDaprID(id string)
	// SetOutputLevel sets log output level
	SetOutputLevel(outputLevel LogLevel)

	// WithLogType specify the log_type field in log. Default value is LogTypeLog
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
	// name is the name of logger that is published to log as a scope
	name string
	// jsonFormatEnabled is a flag to turn on JSON formatted log
	jsonFormatEnabled bool
	// loger is the instance of logrus logger
	logger *logrus.Entry
}

// globalLoggers is the collection of Dapr Logger that is shared globally.
// TODO: User will disable or enable logger on demand.
var globalLoggers = map[string]Logger{}
var globalLoggersLock = sync.RWMutex{}

// NewLogger creates new Logger instance.
func NewLogger(name string) Logger {
	globalLoggersLock.Lock()
	defer globalLoggersLock.Unlock()

	logger, ok := globalLoggers[name]
	if !ok {
		newLogger := logrus.New()
		dl := &daprLogger{
			name:              name,
			jsonFormatEnabled: defaultJSONOutput,
			logger:            defaultLogger(newLogger, name),
		}
		dl.EnableJSONOutput(defaultJSONOutput)
		globalLoggers[name] = dl
		logger = globalLoggers[name]
	}

	return logger
}

func defaultLogger(logger *logrus.Logger, name string) *logrus.Entry {
	return logger.WithFields(logrus.Fields{
		logFieldScope: name,
		logFieldType:  LogTypeLog,
	})
}

func getLoggers() map[string]Logger {
	globalLoggersLock.RLock()
	defer globalLoggersLock.RUnlock()

	l := map[string]Logger{}
	for k, v := range globalLoggers {
		l[k] = v
	}

	return l
}

// EnableJSONOutput enables JSON formatted output log
func (l *daprLogger) EnableJSONOutput(enabled bool) {
	var formatter logrus.Formatter

	l.jsonFormatEnabled = enabled
	fieldMap := logrus.FieldMap{
		// If time field name is conflicted, logrus adds "fields." prefix.
		// So rename to unused field @time to avoid the confliction.
		logrus.FieldKeyTime:  "@time",
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

// SetDaprID sets dapr_id field in log. Default value is empty string
func (l *daprLogger) SetDaprID(id string) {
	if l.jsonFormatEnabled {
		l.logger.Data[logFieldDaprID] = id
	}
}

// SetOutputLevel sets log output level
func (l *daprLogger) SetOutputLevel(outputLevel LogLevel) {
	lvl, ok := daprLogLevelToLogrusLevel[outputLevel]
	if !ok {
		lvl = daprLogLevelToLogrusLevel[defaultOutputLevel]
	}
	l.logger.Logger.SetLevel(lvl)
}

// WithLogType specify the log_type field in log. Default value is LogTypeLog
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
	l.logger.Log(logrus.InfoLevel, args...)
}

// Infof logs a message at level Info.
func (l *daprLogger) Infof(format string, args ...interface{}) {
	if l.jsonFormatEnabled {
		l.logger.Data[logFieldTimeStamp] = utils.ToISO8601DateTimeString(time.Now())
	}
	l.logger.Logf(logrus.InfoLevel, format, args...)
}

// Debug logs a message at level Debug.
func (l *daprLogger) Debug(args ...interface{}) {
	if l.jsonFormatEnabled {
		l.logger.Data[logFieldTimeStamp] = utils.ToISO8601DateTimeString(time.Now())
	}
	l.logger.Log(logrus.DebugLevel, args...)
}

// Debugf logs a message at level Debug.
func (l *daprLogger) Debugf(format string, args ...interface{}) {
	if l.jsonFormatEnabled {
		l.logger.Data[logFieldTimeStamp] = utils.ToISO8601DateTimeString(time.Now())
	}
	l.logger.Logf(logrus.DebugLevel, format, args...)
}

// Warn logs a message at level Warn.
func (l *daprLogger) Warn(args ...interface{}) {
	if l.jsonFormatEnabled {
		l.logger.Data[logFieldTimeStamp] = utils.ToISO8601DateTimeString(time.Now())
	}
	l.logger.Log(logrus.WarnLevel, args...)
}

// Warnf logs a message at level Warn.
func (l *daprLogger) Warnf(format string, args ...interface{}) {
	if l.jsonFormatEnabled {
		l.logger.Data[logFieldTimeStamp] = utils.ToISO8601DateTimeString(time.Now())
	}
	l.logger.Logf(logrus.WarnLevel, format, args...)
}

// Error logs a message at level Error.
func (l *daprLogger) Error(args ...interface{}) {
	if l.jsonFormatEnabled {
		l.logger.Data[logFieldTimeStamp] = utils.ToISO8601DateTimeString(time.Now())
	}
	l.logger.Log(logrus.ErrorLevel, args...)
}

// Errorf logs a message at level Error.
func (l *daprLogger) Errorf(format string, args ...interface{}) {
	if l.jsonFormatEnabled {
		l.logger.Data[logFieldTimeStamp] = utils.ToISO8601DateTimeString(time.Now())
	}
	l.logger.Logf(logrus.ErrorLevel, format, args...)
}

// Fatal logs a message at level Fatal then the process will exit with status set to 1.
func (l *daprLogger) Fatal(args ...interface{}) {
	if l.jsonFormatEnabled {
		l.logger.Data[logFieldTimeStamp] = utils.ToISO8601DateTimeString(time.Now())
	}
	l.logger.Log(logrus.FatalLevel, args...)
}

// Fatalf logs a message at level Fatal then the process will exit with status set to 1.
func (l *daprLogger) Fatalf(format string, args ...interface{}) {
	if l.jsonFormatEnabled {
		l.logger.Data[logFieldTimeStamp] = utils.ToISO8601DateTimeString(time.Now())
	}
	l.logger.Logf(logrus.FatalLevel, format, args...)
}
