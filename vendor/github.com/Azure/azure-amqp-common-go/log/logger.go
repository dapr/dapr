package log

import (
	"context"

	"go.opencensus.io/trace"
)

type (
	// Logger is the interface for opentracing logging
	Logger interface {
		Info(msg string, attributes ...trace.Attribute)
		Error(err error, attributes ...trace.Attribute)
		Fatal(msg string, attributes ...trace.Attribute)
		Debug(msg string, attributes ...trace.Attribute)
	}

	spanLogger struct {
		span *trace.Span
	}

	nopLogger struct{}
)

// For will return a logger for a given context
func For(ctx context.Context) Logger {
	if span := trace.FromContext(ctx); span != nil {
		return &spanLogger{
			span: span,
		}
	}
	return new(nopLogger)
}

func (sl spanLogger) Info(msg string, attributes ...trace.Attribute) {
	sl.logToSpan("info", msg, attributes...)
}

func (sl spanLogger) Error(err error, attributes ...trace.Attribute) {
	attributes = append(attributes, trace.BoolAttribute("error", true))
	sl.logToSpan("error", err.Error(), attributes...)
}

func (sl spanLogger) Fatal(msg string, attributes ...trace.Attribute) {
	attributes = append(attributes, trace.BoolAttribute("error", true))
	sl.logToSpan("fatal", msg, attributes...)
}

func (sl spanLogger) Debug(msg string, attributes ...trace.Attribute) {
	sl.logToSpan("debug", msg, attributes...)
}

func (sl spanLogger) logToSpan(level string, msg string, attributes ...trace.Attribute) {
	attrs := append(attributes, trace.StringAttribute("event", msg), trace.StringAttribute("level", level))
	sl.span.AddAttributes(attrs...)
}

func (sl nopLogger) Info(msg string, attributes ...trace.Attribute)  {}
func (sl nopLogger) Error(err error, attributes ...trace.Attribute)  {}
func (sl nopLogger) Fatal(msg string, attributes ...trace.Attribute) {}
func (sl nopLogger) Debug(msg string, attributes ...trace.Attribute) {}
