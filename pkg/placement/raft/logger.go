// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package raft

import (
	"io"
	"log"

	"github.com/hashicorp/go-hclog"

	"github.com/dapr/kit/logger"
)

var logging = logger.NewLogger("dapr.placement.raft")

func newLoggerAdapter() hclog.Logger {
	return &loggerAdapter{}
}

// loggerAdapter is the adapter to integrate with dapr logger.
type loggerAdapter struct{}

func (l *loggerAdapter) Log(level hclog.Level, msg string, args ...interface{}) {
	switch level {
	case hclog.Debug:
		logging.Debugf(msg, args)
	case hclog.Warn:
		logging.Debugf(msg, args)
	case hclog.Error:
		logging.Debugf(msg, args)
	default:
		logging.Debugf(msg, args)
	}
}

func (l *loggerAdapter) Trace(msg string, args ...interface{}) {
	logging.Debugf(msg, args)
}

func (l *loggerAdapter) Debug(msg string, args ...interface{}) {
	logging.Debugf(msg, args)
}

func (l *loggerAdapter) Info(msg string, args ...interface{}) {
	logging.Debugf(msg, args)
}

func (l *loggerAdapter) Warn(msg string, args ...interface{}) {
	logging.Debugf(msg, args)
}

func (l *loggerAdapter) Error(msg string, args ...interface{}) {
	logging.Debugf(msg, args)
}

func (l *loggerAdapter) IsTrace() bool { return false }

func (l *loggerAdapter) IsDebug() bool { return true }

func (l *loggerAdapter) IsInfo() bool { return false }

func (l *loggerAdapter) IsWarn() bool { return false }

func (l *loggerAdapter) IsError() bool { return false }

func (l *loggerAdapter) ImpliedArgs() []interface{} { return []interface{}{} }

func (l *loggerAdapter) With(args ...interface{}) hclog.Logger { return l }

func (l *loggerAdapter) Name() string { return "dapr" }

func (l *loggerAdapter) Named(name string) hclog.Logger { return l }

func (l *loggerAdapter) ResetNamed(name string) hclog.Logger { return l }

func (l *loggerAdapter) SetLevel(level hclog.Level) {}

func (l *loggerAdapter) StandardLogger(opts *hclog.StandardLoggerOptions) *log.Logger {
	return log.New(l.StandardWriter(opts), "placement-raft", log.LstdFlags)
}

func (l *loggerAdapter) StandardWriter(opts *hclog.StandardLoggerOptions) io.Writer {
	return io.Discard
}
