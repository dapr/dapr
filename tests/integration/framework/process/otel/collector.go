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

package otel

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.opentelemetry.io/otel/trace"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
)

// Option is a function that configures the process.
type Option func(*options)

// Collector is an in-memory OTLP collector for testing.
type Collector struct {
	coltracepb.UnimplementedTraceServiceServer
	listener    net.Listener
	server      *grpc.Server
	spans       []*tracepb.ResourceSpans
	mu          sync.RWMutex
	srvErrCh    chan error
	runOnce     sync.Once
	cleanupOnce sync.Once
	ports       *ports.Ports
}

// options contains the options for running an OTLP collector.
type options struct{}

func New(t *testing.T, fopts ...Option) *Collector {
	t.Helper()

	opts := options{}
	for _, fopt := range fopts {
		fopt(&opts)
	}

	fp := ports.Reserve(t, 1)
	listener := fp.Listener(t)
	server := grpc.NewServer()
	collector := &Collector{
		listener: listener,
		server:   server,
		spans:    make([]*tracepb.ResourceSpans, 0),
		srvErrCh: make(chan error),
		ports:    fp,
	}

	// Register the trace service
	coltracepb.RegisterTraceServiceServer(server, collector)
	return collector
}

func (c *Collector) Run(t *testing.T, ctx context.Context) {
	c.runOnce.Do(func() {
		errCh := make(chan error, 1)
		go func(ctx context.Context) {
			select {
			case errCh <- c.server.Serve(c.listener):
			case <-ctx.Done():
				return
			}
		}(ctx)
		go func(ctx context.Context) {
			select {
			case err := <-errCh:
				c.srvErrCh <- err
			case <-ctx.Done():
				c.server.GracefulStop()
				c.srvErrCh <- ctx.Err()
			}
		}(ctx)
	})
}

func (c *Collector) WaitUntilRunning(t *testing.T, ctx context.Context) {
	t.Helper()

	require.EventuallyWithT(t, func(co *assert.CollectT) {
		conn, err := grpc.NewClient(c.OTLPGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if !assert.NoError(co, err) {
			return
		}
		defer conn.Close()

		// Actually test the connection by checking its state
		conn.Connect()
		state := conn.GetState()
		assert.NotEqual(co, connectivity.TransientFailure, state, "connection should not be in transient failure state")
		assert.NotEqual(co, connectivity.Shutdown, state, "connection should not be shutdown")
	}, 20*time.Second, 10*time.Millisecond)
}

// Cleanup implements framework.Process
func (c *Collector) Cleanup(t *testing.T) {
	c.cleanupOnce.Do(func() {
		c.ports.Free(t)
		c.server.GracefulStop()
		<-c.srvErrCh
	})
}

// Export implements the OTLP trace service Export method
func (c *Collector) Export(ctx context.Context, req *coltracepb.ExportTraceServiceRequest) (*coltracepb.ExportTraceServiceResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.spans = append(c.spans, req.GetResourceSpans()...)
	return &coltracepb.ExportTraceServiceResponse{}, nil
}

// ExportTraces implements the OTLP trace service ExportTraces method
func (c *Collector) ExportTraces(ctx context.Context, req *coltracepb.ExportTraceServiceRequest) (*coltracepb.ExportTraceServiceResponse, error) {
	return c.Export(ctx, req)
}

// GetSpans returns a copy of all received spans
func (c *Collector) GetSpans() []*tracepb.ResourceSpans {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]*tracepb.ResourceSpans, len(c.spans))
	copy(result, c.spans)
	return result
}

// OTLPGRPCAddress returns the gRPC endpoint address for the collector
func (c *Collector) OTLPGRPCAddress() string {
	return c.listener.Addr().String()
}

func (c *Collector) GRPCDaprdConfiguration(t *testing.T) daprd.Option {
	t.Helper()
	return daprd.WithConfigManifests(t, fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: tracing
spec:
  tracing:
    samplingRate: "1.0"
    otel:
      endpointAddress: "%s"
      protocol: grpc
      isSecure: false
`, c.OTLPGRPCAddress()))
}

func (c *Collector) GRPCProvider(t *testing.T, ctx context.Context) *sdktrace.TracerProvider {
	t.Helper()

	client := otlptracegrpc.NewClient(
		otlptracegrpc.WithEndpoint(c.OTLPGRPCAddress()),
		otlptracegrpc.WithInsecure(),
	)

	exp, err := otlptrace.New(ctx, client)
	require.NoError(t, err)

	res, err := resource.New(ctx, resource.WithAttributes(semconv.ServiceName(t.Name())))
	require.NoError(t, err)

	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
}

func (c *Collector) TraceSpans(traceID trace.TraceID) []*tracepb.Span {
	var spans []*tracepb.Span
	for _, span := range c.GetSpans() {
		for _, scopeSpan := range span.GetScopeSpans() {
			for _, span := range scopeSpan.GetSpans() {
				if hex.EncodeToString(span.GetTraceId()) == traceID.String() {
					spans = append(spans, span)
				}
			}
		}
	}

	sort.SliceStable(spans, func(i, j int) bool {
		return spans[i].GetStartTimeUnixNano() < spans[j].GetStartTimeUnixNano()
	})

	return spans
}
