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

package runtime

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// tracerProviderStore allows us to capture the trace provider options
// and set a trace provider as per those settings
//
// This is needed because the OpenTelemetry does not allow accessing
// tracer provider settings after registration
type tracerProviderStore interface {
	// RegisterOptions registers a trace.Exporter.
	RegisterExporter(exporter sdktrace.SpanExporter)
	RegisterResource(res *resource.Resource)
	RegisterSampler(sampler sdktrace.Sampler)
	RegisterTracerProvider() *sdktrace.TracerProvider
	HasExporter() bool
}

// newOpentelemetryTracerProviderStore returns an opentelemetryOptionsStore
func newOpentelemetryTracerProviderStore() *opentelemetryTracerProviderStore {
	exps := []sdktrace.SpanExporter{}
	return &opentelemetryTracerProviderStore{exps, nil, nil}
}

// opentelemetryOptionsStore is an implementation of traceOptionsStore
type opentelemetryTracerProviderStore struct {
	exporters []sdktrace.SpanExporter
	res       *resource.Resource
	sampler   sdktrace.Sampler
}

// RegisterExporter adds a Span Exporter for registration with open telemetry global trace provider
func (s *opentelemetryTracerProviderStore) RegisterExporter(exporter sdktrace.SpanExporter) {
	s.exporters = append(s.exporters, exporter)
}

// HasExporter returns whether at least one Span Exporter has been registered
func (s *opentelemetryTracerProviderStore) HasExporter() bool {
	return len(s.exporters) > 0
}

// RegisterResource adds a Resource for registration with open telemetry global trace provider
func (s *opentelemetryTracerProviderStore) RegisterResource(res *resource.Resource) {
	s.res = res
}

// RegisterSampler adds a custom sampler for registration with open telemetry global trace provider
func (s *opentelemetryTracerProviderStore) RegisterSampler(sampler sdktrace.Sampler) {
	s.sampler = sampler
}

// RegisterTraceProvider registers a trace provider as per the tracer options in the store
func (s *opentelemetryTracerProviderStore) RegisterTracerProvider() *sdktrace.TracerProvider {
	if len(s.exporters) != 0 {
		tracerOptions := []sdktrace.TracerProviderOption{}
		for _, exporter := range s.exporters {
			tracerOptions = append(tracerOptions, sdktrace.WithBatcher(exporter))
		}

		if s.res != nil {
			tracerOptions = append(tracerOptions, sdktrace.WithResource(s.res))
		}

		if s.sampler != nil {
			tracerOptions = append(tracerOptions, sdktrace.WithSampler(s.sampler))
		}

		tp := sdktrace.NewTracerProvider(tracerOptions...)

		otel.SetTracerProvider(tp)
		return tp
	}
	return nil
}

// fakeTracerOptionsStore implements tracerOptionsStore by merely record the exporters
// and config that were registered/applied.
//
// This is only for use in unit tests.
type fakeTracerProviderStore struct {
	exporters []sdktrace.SpanExporter
	res       *resource.Resource
	sampler   sdktrace.Sampler
}

// newFakeTracerProviderStore returns an opentelemetryOptionsStore
func newFakeTracerProviderStore() *fakeTracerProviderStore {
	exps := []sdktrace.SpanExporter{}
	return &fakeTracerProviderStore{exps, nil, nil}
}

// RegisterExporter adds a Span Exporter for registration with open telemetry global trace provider
func (s *fakeTracerProviderStore) RegisterExporter(exporter sdktrace.SpanExporter) {
	s.exporters = append(s.exporters, exporter)
}

// RegisterResource adds a Resource for registration with open telemetry global trace provider
func (s *fakeTracerProviderStore) RegisterResource(res *resource.Resource) {
	s.res = res
}

// RegisterSampler adds a custom sampler for registration with open telemetry global trace provider
func (s *fakeTracerProviderStore) RegisterSampler(sampler sdktrace.Sampler) {
	s.sampler = sampler
}

// RegisterTraceProvider does nothing
func (s *fakeTracerProviderStore) RegisterTracerProvider() *sdktrace.TracerProvider { return nil }

// HasExporter returns whether at least one Span Exporter has been registered
func (s *fakeTracerProviderStore) HasExporter() bool {
	return len(s.exporters) > 0
}
