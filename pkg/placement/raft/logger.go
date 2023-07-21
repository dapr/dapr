/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package raft

import (
	"io"
	"log"
	"strings"

	"github.com/hashicorp/go-hclog"
	"github.com/spf13/cast"

	"github.com/dapr/kit/logger"
)

var logging = logger.NewLogger("dapr.placement.raft")

func newLoggerAdapter() hclog.Logger {
	return &loggerAdapter{}
}

// loggerAdapter is the adapter to integrate with dapr logger.
type loggerAdapter struct{}

func (l *loggerAdapter) printLog(msg string, args ...any) {
	if len(args) > 0 {
		fields := strings.Builder{}
		for i, f := range args {
			if i%2 == 1 {
				fields.WriteRune('=')
			} else {
				fields.WriteRune(',')
			}
			fields.WriteString(cast.ToString(f))
		}
		logging.Debug(msg + fields.String())
		return
	}

	logging.Debug(msg)
}

func (l *loggerAdapter) Log(level hclog.Level, msg string, args ...interface{}) {
	switch level {
	case hclog.Debug:
		l.printLog(msg, args...)
	case hclog.Warn:
		l.printLog(msg, args...)
	case hclog.Error:
		l.printLog(msg, args...)
	default:
		l.printLog(msg, args...)
	}
}

func (l *loggerAdapter) Trace(msg string, args ...interface{}) {
	l.printLog(msg, args...)
}

func (l *loggerAdapter) Debug(msg string, args ...interface{}) {
	l.printLog(msg, args...)
}

func (l *loggerAdapter) Info(msg string, args ...interface{}) {
	l.printLog(msg, args...)
}

func (l *loggerAdapter) Warn(msg string, args ...interface{}) {
	l.printLog(msg, args...)
}

func (l *loggerAdapter) Error(msg string, args ...interface{}) {
	l.printLog(msg, args...)
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

func (l *loggerAdapter) GetLevel() hclog.Level {
	return hclog.Info
}

func (l *loggerAdapter) StandardLogger(opts *hclog.StandardLoggerOptions) *log.Logger {
	return log.New(l.StandardWriter(opts), "placement-raft", log.LstdFlags)
}

func (l *loggerAdapter) StandardWriter(opts *hclog.StandardLoggerOptions) io.Writer {
	return io.Discard
}
