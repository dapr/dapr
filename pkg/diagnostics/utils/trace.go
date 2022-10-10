package utils

import (
	"encoding/hex"
	"fmt"
	"regexp"

	"go.opentelemetry.io/otel/trace"
)

const (
	maxVersion       = 254
	supportedVersion = 0

	TraceparentHeader = "traceparent"
	TracestateHeader  = "tracestate"
)

var traceCtxRegExp = regexp.MustCompile("^(?P<version>[0-9a-f]{2})-(?P<traceID>[a-f0-9]{32})-(?P<spanID>[a-f0-9]{16})-(?P<traceFlags>[a-f0-9]{2})(?:-.*)?$")

// SpanContextFromW3CString generates span context by traceparent.
func SpanContextFromW3CString(traceparent string) trace.SpanContext {
	matches := traceCtxRegExp.FindStringSubmatch(traceparent)

	if len(matches) == 0 {
		return trace.SpanContext{}
	}

	if len(matches) < 5 { // four subgroups plus the overall match
		return trace.SpanContext{}
	}

	if len(matches[1]) != 2 {
		return trace.SpanContext{}
	}
	ver, err := hex.DecodeString(matches[1])
	if err != nil {
		return trace.SpanContext{}
	}
	version := int(ver[0])
	if version > maxVersion {
		return trace.SpanContext{}
	}

	if version == 0 && len(matches) != 5 { // four subgroups plus the overall match
		return trace.SpanContext{}
	}

	if len(matches[2]) != 32 {
		return trace.SpanContext{}
	}

	var scc trace.SpanContextConfig

	scc.TraceID, err = trace.TraceIDFromHex(matches[2][:32])
	if err != nil {
		return trace.SpanContext{}
	}

	if len(matches[3]) != 16 {
		return trace.SpanContext{}
	}
	scc.SpanID, err = trace.SpanIDFromHex(matches[3])
	if err != nil {
		return trace.SpanContext{}
	}

	if len(matches[4]) != 2 {
		return trace.SpanContext{}
	}
	opts, err := hex.DecodeString(matches[4])
	if err != nil || len(opts) < 1 || (version == 0 && opts[0] > 2) {
		return trace.SpanContext{}
	}
	// Clear all flags other than the trace-context supported sampling bit.
	scc.TraceFlags = trace.TraceFlags(opts[0]) & trace.FlagsSampled

	sc := trace.NewSpanContext(scc)
	if !sc.IsValid() {
		return trace.SpanContext{}
	}

	return sc
}

// TraceStateFromW3CString generates tracestate.
func TraceStateFromW3CString(tracestate string) trace.TraceState {
	if tracestate == "" {
		return trace.TraceState{}
	}

	ts, err := trace.ParseTraceState(tracestate)
	if err != nil {
		return trace.TraceState{}
	}

	return ts
}

// SpanContextToMetadata adds the spancontext in traceparent and tracestate headers.
func SpanContextToMetadata(sc trace.SpanContext, setHeader func(string, string)) {
	if !sc.IsValid() {
		return
	}

	traceparent := TraceparentToW3CString(sc)
	tracestate := TraceStateToW3CString(sc)
	setHeader(TraceparentHeader, traceparent)
	setHeader(TracestateHeader, tracestate)
}

// TraceparentToW3CString gets traceparent from spancontext.
func TraceparentToW3CString(sc trace.SpanContext) string {
	flags := sc.TraceFlags() & trace.FlagsSampled

	return fmt.Sprintf("%.2x-%s-%s-%s",
		supportedVersion,
		sc.TraceID(),
		sc.SpanID(),
		flags)
}

// TraceStateToW3CString extracts the TraceState from given SpanContext and returns its string representation.
func TraceStateToW3CString(sc trace.SpanContext) string {
	return sc.TraceState().String()
}
