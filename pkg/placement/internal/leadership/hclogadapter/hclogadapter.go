/*
Copyright 2026 The Dapr Authors
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

// Package hclogadapter adapts the Dapr logger to hclog.Logger, which is the
// only logging interface hashicorp/raft accepts.
package hclogadapter

import (
	"io"
	"log"
	"log/slog"

	"github.com/hashicorp/go-hclog"

	"github.com/dapr/kit/logger"
)

var logging = logger.New("dapr.placement.leadership.raft")

func New() hclog.Logger {
	return &loggerAdapter{log: logging}
}

// loggerAdapter renders raft's records through the structured Dapr logger.
// hclog already passes a message plus alternating keys and values, so they map
// straight onto attributes; the previous adapter concatenated them into the
// message text.
type loggerAdapter struct {
	log *logger.Log
}

// levels maps hclog levels onto Dapr levels. raft's routine chatter has always
// been emitted at debug in Dapr regardless of hclog level, and that is
// preserved for warnings and below so log volume does not change; errors now
// surface at error level instead of being buried at debug.
func (l *loggerAdapter) Log(level hclog.Level, msg string, args ...any) {
	switch level {
	case hclog.Error:
		l.log.Error(msg, args...)
	default:
		l.log.Debug(msg, args...)
	}
}

func (l *loggerAdapter) Trace(msg string, args ...any) {
	l.log.Debug(msg, args...)
}

func (l *loggerAdapter) Debug(msg string, args ...any) {
	l.log.Debug(msg, args...)
}

func (l *loggerAdapter) Info(msg string, args ...any) {
	l.log.Debug(msg, args...)
}

func (l *loggerAdapter) Warn(msg string, args ...any) {
	l.log.Debug(msg, args...)
}

func (l *loggerAdapter) Error(msg string, args ...any) {
	l.log.Error(msg, args...)
}

func (l *loggerAdapter) IsTrace() bool { return false }

func (l *loggerAdapter) IsDebug() bool { return true }

func (l *loggerAdapter) IsInfo() bool { return false }

func (l *loggerAdapter) IsWarn() bool { return false }

func (l *loggerAdapter) IsError() bool { return true }

func (l *loggerAdapter) ImpliedArgs() []any { return []any{} }

// With returns an adapter carrying the given attributes on every record,
// instead of dropping them as the previous implementation did.
func (l *loggerAdapter) With(args ...any) hclog.Logger {
	return &loggerAdapter{log: l.log.With(args...)}
}

func (l *loggerAdapter) Name() string { return "dapr" }

func (l *loggerAdapter) Named(name string) hclog.Logger {
	return &loggerAdapter{log: l.log.With("name", name)}
}

func (l *loggerAdapter) ResetNamed(name string) hclog.Logger {
	return &loggerAdapter{log: logging.With("name", name)}
}

func (l *loggerAdapter) SetLevel(level hclog.Level) {}

func (l *loggerAdapter) GetLevel() hclog.Level {
	return hclog.Info
}

func (l *loggerAdapter) StandardLogger(opts *hclog.StandardLoggerOptions) *log.Logger {
	return slog.NewLogLogger(l.log.Handler(), slog.LevelDebug)
}

func (l *loggerAdapter) StandardWriter(opts *hclog.StandardLoggerOptions) io.Writer {
	return io.Discard
}
