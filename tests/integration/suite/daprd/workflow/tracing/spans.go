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

package tracing

import (
	v1 "go.opentelemetry.io/proto/otlp/trace/v1"
)

// workflowSpanNames returns the span names, leaving out spans for calls into
// the state store, whose number depends on how the backend batches writes.
func workflowSpanNames(spans []*v1.Span) []string {
	names := make([]string, 0, len(spans))
	for _, span := range spans {
		if isComponentSpan(span) {
			continue
		}
		names = append(names, span.GetName())
	}
	return names
}

func isComponentSpan(span *v1.Span) bool {
	for _, kv := range span.GetAttributes() {
		if kv.GetKey() == "dapr.component.name" {
			return true
		}
	}
	return false
}
