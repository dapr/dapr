package logging

import "github.com/go-logr/logr"

// NullLogger is a logr.Logger that does nothing
var NullLogger = logr.New(nullLogSink{})

type nullLogSink struct{}

var _ logr.LogSink = nullLogSink{}

func (nullLogSink) Init(_ logr.RuntimeInfo) {
	// Do nothing.
}

func (nullLogSink) Enabled(_ int) bool {
	return false
}

func (nullLogSink) Info(_ int, _ string, _ ...interface{}) {
	// Do nothing.
}

func (nullLogSink) Error(_ error, _ string, _ ...interface{}) {
	// Do nothing.
}

func (log nullLogSink) WithValues(_ ...interface{}) logr.LogSink {
	return log
}

func (log nullLogSink) WithName(name string) logr.LogSink {
	return log
}
