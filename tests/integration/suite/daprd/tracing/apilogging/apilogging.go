/*
Copyright 2025 The Dapr Authors
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

package apilogging

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// spanIDField matches the span_id field of an API log line, in either the text
// or the JSON log format.
var spanIDField = regexp.MustCompile(`span_id[=:]"?([0-9a-f]{16})"?`)

// newTraceparent returns a random trace ID and a traceparent header carrying it.
func newTraceparent(t *testing.T) (trace.TraceID, string) {
	t.Helper()

	var (
		traceID trace.TraceID
		spanID  trace.SpanID
	)
	_, err := rand.Read(traceID[:])
	require.NoError(t, err)
	_, err = rand.Read(spanID[:])
	require.NoError(t, err)

	return traceID, "00-" + traceID.String() + "-" + spanID.String() + "-01"
}

// apiLogLine returns the API log line that daprd wrote for the given trace, or
// an empty string if it has not written one yet.
func apiLogLine(stdout []byte, msg string, traceID trace.TraceID) string {
	for line := range strings.SplitSeq(string(stdout), "\n") {
		if strings.Contains(line, msg) && strings.Contains(line, traceID.String()) {
			return line
		}
	}
	return ""
}

// logSpanID returns the span ID that the given API log line reports.
func logSpanID(t *testing.T, line string) string {
	t.Helper()

	match := spanIDField.FindStringSubmatch(line)
	require.Len(t, match, 2, "no span_id field in API log line: %s", line)
	return match[1]
}

// exportedSpanIDs returns the IDs of the given exported spans.
func exportedSpanIDs(spans []*tracepb.Span) []string {
	ids := make([]string, 0, len(spans))
	for _, span := range spans {
		ids = append(ids, hex.EncodeToString(span.GetSpanId()))
	}
	return ids
}
