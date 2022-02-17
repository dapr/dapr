package logging

import "github.com/go-logr/logr"

// NullLogSink is a logr.LogSink that does nothing.
type NullLogSink struct{}

func (NullLogSink) Init(_ logr.RuntimeInfo) {
	// Do nothing.
}

func (NullLogSink) Enabled(_ int) bool {
	return false
}

func (NullLogSink) Info(_ int, _ string, _ ...interface{}) {
	// Do nothing.
}

func (NullLogSink) Error(_ error, _ string, _ ...interface{}) {
	// Do nothing.
}

func (log NullLogSink) WithValues(_ ...interface{}) logr.LogSink {
	return log
}

func (log NullLogSink) WithName(name string) logr.LogSink {
	return log
}
