// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package trace // import "go.opentelemetry.io/otel/sdk/trace"

import (
	"encoding/binary"
	"fmt"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type traceIDParentAndRatioSampler struct {
	traceIDUpperBound uint64
	description       string
}

func (ts traceIDParentAndRatioSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	psc := trace.SpanContextFromContext(p.ParentContext)
	if psc.IsSampled() {
		return sdktrace.SamplingResult{
			Decision:   sdktrace.RecordAndSample,
			Tracestate: psc.TraceState(),
		}
	}
	x := binary.BigEndian.Uint64(p.TraceID[0:8]) >> 1
	if x < ts.traceIDUpperBound {
		return sdktrace.SamplingResult{
			Decision:   sdktrace.RecordAndSample,
			Tracestate: psc.TraceState(),
		}
	}
	return sdktrace.SamplingResult{
		Decision:   sdktrace.Drop,
		Tracestate: psc.TraceState(),
	}
}

func (ts traceIDParentAndRatioSampler) Description() string {
	return ts.description
}

// TraceIDBasedParentAndRatio samples a given fraction of traces and parent sampler.
func TraceIDBasedParentAndRatio(fraction float64) sdktrace.Sampler {
	if fraction >= 1 {
		return sdktrace.AlwaysSample()
	}

	if fraction <= 0 {
		fraction = 0
	}

	return &traceIDParentAndRatioSampler{
		traceIDUpperBound: uint64(fraction * (1 << 63)),
		description:       fmt.Sprintf("TraceIDRatioBased{%g}", fraction),
	}
}
