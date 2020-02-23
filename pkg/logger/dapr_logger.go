package logger

import (
	"os"
	"time"

	"github.com/dapr/dapr/pkg/version"
	"github.com/dapr/dapr/utils"
	"github.com/sirupsen/logrus"
)

// daprLogger is the implemention for logrus
type daprLogger struct {
	// name is the name of logger that is published to log as a scope
	name string
	// jsonFormatEnabled is a flag to turn on JSON formatted log
	jsonFormatEnabled bool
	// loger is the instance of logrus logger
	logger *logrus.Entry
}

func newDaprLogger(name string) *daprLogger {
	newLogger := logrus.New()

	dl := &daprLogger{
		name:              name,
		jsonFormatEnabled: defaultJSONOutput,
		logger: newLogger.WithFields(logrus.Fields{
			logFieldScope: name,
			logFieldType:  LogTypeLog,
		}),
	}

	dl.EnableJSONOutput(defaultJSONOutput)

	return dl
}

// EnableJSONOutput enables JSON formatted output log
func (l *daprLogger) EnableJSONOutput(enabled bool) error {
	var formatter logrus.Formatter

	l.jsonFormatEnabled = enabled
	fieldMap := logrus.FieldMap{
		// If time field name is conflicted, logrus adds "fields." prefix.
		// So rename to unused field @time to avoid the confliction.
		logrus.FieldKeyTime:  "@time",
		logrus.FieldKeyLevel: logFieldLevel,
		logrus.FieldKeyMsg:   logFieldMessage,
	}

	l.logger.Data = logrus.Fields{
		logFieldScope: l.logger.Data[logFieldScope],
		logFieldType:  LogTypeLog,
	}

	if enabled {
		hostname, _ := os.Hostname()
		l.logger.Data[logFieldInstance] = hostname
		l.logger.Data[logFieldDaprVer] = version.Version()

		formatter = &logrus.JSONFormatter{
			DisableTimestamp: true,
			FieldMap:         fieldMap,
		}
	} else {
		formatter = &logrus.TextFormatter{
			DisableTimestamp: true,
			FieldMap:         fieldMap,
		}
	}

	l.logger.Logger.SetFormatter(formatter)

	return nil
}

// SetDaprID sets dapr_id field in log. Default value is empty string
func (l *daprLogger) SetDaprID(id string) {
	if l.jsonFormatEnabled {
		l.logger.Data[logFieldDaprID] = id
	}
}

func toLogrusLevel(lvl LogLevel) logrus.Level {
	// ignore error because it will never happens
	l, _ := logrus.ParseLevel(string(lvl))
	return l
}

// SetOutputLevel sets log output level
func (l *daprLogger) SetOutputLevel(outputLevel LogLevel) error {
	l.logger.Logger.SetLevel(toLogrusLevel(outputLevel))

	return nil
}

// WithLogType specify the log_type field in log. Default value is LogTypeLog
func (l *daprLogger) WithLogType(logType string) Logger {
	return &daprLogger{
		name:              l.name,
		jsonFormatEnabled: l.jsonFormatEnabled,
		logger:            l.logger.WithField(logFieldType, logType),
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
	l.logger.Fatal(args...)
}

// Fatalf logs a message at level Fatal then the process will exit with status set to 1.
func (l *daprLogger) Fatalf(format string, args ...interface{}) {
	if l.jsonFormatEnabled {
		l.logger.Data[logFieldTimeStamp] = utils.ToISO8601DateTimeString(time.Now())
	}
	l.logger.Fatalf(format, args...)
}
