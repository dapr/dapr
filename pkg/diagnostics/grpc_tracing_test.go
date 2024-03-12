/*
Copyright 2023 The Dapr Authors
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

package diagnostics

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcMetadata "google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/api/grpc/metadata"
	"github.com/dapr/dapr/pkg/config"
	diagConsts "github.com/dapr/dapr/pkg/diagnostics/consts"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func TestSpanAttributesMapFromGRPC(t *testing.T) {
	tests := []struct {
		rpcMethod                    string
		req                          any
		expectedServiceNameAttribute string
		expectedCustomAttribute      string
	}{
		{"/dapr.proto.runtime.v1.Dapr/InvokeService", &runtimev1pb.InvokeServiceRequest{Message: &commonv1pb.InvokeRequest{Method: "mymethod"}}, "ServiceInvocation", "mymethod"},
		{"/dapr.proto.runtime.v1.Dapr/GetState", &runtimev1pb.GetStateRequest{StoreName: "mystore"}, "Dapr", "mystore"},
		{"/dapr.proto.runtime.v1.Dapr/SaveState", &runtimev1pb.SaveStateRequest{StoreName: "mystore"}, "Dapr", "mystore"},
		{"/dapr.proto.runtime.v1.Dapr/DeleteState", &runtimev1pb.DeleteStateRequest{StoreName: "mystore"}, "Dapr", "mystore"},
		{"/dapr.proto.runtime.v1.Dapr/GetSecret", &runtimev1pb.GetSecretRequest{StoreName: "mysecretstore"}, "Dapr", "mysecretstore"},
		{"/dapr.proto.runtime.v1.Dapr/InvokeBinding", &runtimev1pb.InvokeBindingRequest{Name: "mybindings"}, "Dapr", "mybindings"},
		{"/dapr.proto.runtime.v1.Dapr/PublishEvent", &runtimev1pb.PublishEventRequest{Topic: "mytopic"}, "Dapr", "mytopic"},
		{"/dapr.proto.runtime.v1.Dapr/BulkPublishEventAlpha1", &runtimev1pb.BulkPublishRequest{Topic: "mytopic"}, "Dapr", "mytopic"},
		// Expecting ServiceInvocation because this call will be treated as client call of service invocation.
		{"/dapr.proto.internals.v1.ServiceInvocation/CallLocal", &internalv1pb.InternalInvokeRequest{Message: &commonv1pb.InvokeRequest{Method: "mymethod"}}, "ServiceInvocation", "mymethod"},
	}
	for _, tt := range tests {
		t.Run(tt.rpcMethod, func(t *testing.T) {
			got := spanAttributesMapFromGRPC("fakeAppID", tt.req, tt.rpcMethod)
			assert.Equal(t, tt.expectedServiceNameAttribute, got[diagConsts.GrpcServiceSpanAttributeKey], "servicename attribute should be equal")
		})
	}
}

func TestUserDefinedMetadata(t *testing.T) {
	md := grpcMetadata.MD{
		"dapr-userdefined-1": []string{"value1"},
		"DAPR-userdefined-2": []string{"value2", "value3"}, // Will be lowercased
		"no-attr":            []string{"value3"},
	}

	testCtx := grpcMetadata.NewIncomingContext(context.Background(), md)
	metadata.SetMetadataInContextUnary(testCtx, nil, nil, func(ctx context.Context, req any) (any, error) {
		testCtx = ctx
		return nil, nil
	})

	m := userDefinedMetadata(testCtx)

	assert.Len(t, m, 2)
	assert.Equal(t, "value1", m["dapr-userdefined-1"])
	assert.Equal(t, "value2", m["dapr-userdefined-2"])
}

func TestSpanContextToGRPCMetadata(t *testing.T) {
	t.Run("empty span context", func(t *testing.T) {
		ctx := context.Background()
		newCtx := SpanContextToGRPCMetadata(ctx, trace.SpanContext{})

		assert.Equal(t, ctx, newCtx)
	})
}

func TestGRPCTraceUnaryServerInterceptor(t *testing.T) {
	exp := newOtelFakeExporter()

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
	)
	defer func() { _ = tp.Shutdown(context.Background()) }()
	otel.SetTracerProvider(tp)

	interceptor := GRPCTraceUnaryServerInterceptor("fakeAppID", config.TracingSpec{SamplingRate: "1"})

	testTraceParent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	testSpanContext, _ := SpanContextFromW3CString(testTraceParent)
	testTraceBinary := diagUtils.BinaryFromSpanContext(testSpanContext)

	t.Run("grpc-trace-bin is given", func(t *testing.T) {
		ctx := grpcMetadata.NewIncomingContext(context.Background(), grpcMetadata.Pairs("grpc-trace-bin", string(testTraceBinary)))
		fakeInfo := &grpc.UnaryServerInfo{
			FullMethod: "/dapr.proto.runtime.v1.Dapr/GetState",
		}
		fakeReq := &runtimev1pb.GetStateRequest{
			StoreName: "statestore",
			Key:       "state",
		}

		var span trace.Span
		assertHandler := func(ctx context.Context, req any) (any, error) {
			span = diagUtils.SpanFromContext(ctx)
			return nil, errors.New("fake error")
		}

		metadata.SetMetadataInContextUnary(ctx, fakeReq, fakeInfo, func(ctx context.Context, req any) (any, error) {
			return interceptor(ctx, fakeReq, fakeInfo, assertHandler)
		})

		sc := span.SpanContext()
		traceID := sc.TraceID()
		assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", hex.EncodeToString(traceID[:]))
		spanID := sc.SpanID()
		assert.NotEqual(t, "00f067aa0ba902b7", hex.EncodeToString(spanID[:]))
	})

	t.Run("grpc-trace-bin is not given", func(t *testing.T) {
		fakeInfo := &grpc.UnaryServerInfo{
			FullMethod: "/dapr.proto.runtime.v1.Dapr/GetState",
		}
		fakeReq := &runtimev1pb.GetStateRequest{
			StoreName: "statestore",
			Key:       "state",
		}

		var span trace.Span
		assertHandler := func(ctx context.Context, req any) (any, error) {
			span = diagUtils.SpanFromContext(ctx)
			return nil, errors.New("fake error")
		}

		interceptor(context.Background(), fakeReq, fakeInfo, assertHandler)

		sc := span.SpanContext()
		traceID := sc.TraceID()
		spanID := sc.SpanID()
		assert.NotEmpty(t, hex.EncodeToString(traceID[:]))
		assert.NotEmpty(t, hex.EncodeToString(spanID[:]))
	})

	t.Run("InvokeService call", func(t *testing.T) {
		fakeInfo := &grpc.UnaryServerInfo{
			FullMethod: "/dapr.proto.runtime.v1.Dapr/InvokeService",
		}
		fakeReq := &runtimev1pb.InvokeServiceRequest{
			Id:      "targetID",
			Message: &commonv1pb.InvokeRequest{Method: "method1"},
		}

		var span trace.Span
		assertHandler := func(ctx context.Context, req any) (any, error) {
			span = diagUtils.SpanFromContext(ctx)
			return nil, errors.New("fake error")
		}

		interceptor(context.Background(), fakeReq, fakeInfo, assertHandler)

		sc := span.SpanContext()
		spanString := fmt.Sprintf("%v", span)
		assert.True(t, strings.Contains(spanString, "CallLocal/targetID/method1"))
		traceID := sc.TraceID()
		spanID := sc.SpanID()
		assert.NotEmpty(t, hex.EncodeToString(traceID[:]))
		assert.NotEmpty(t, hex.EncodeToString(spanID[:]))
	})

	t.Run("InvokeService call with grpc status error", func(t *testing.T) {
		// set a new tracer provider with a callback on span completion to check that the span errors out
		checkErrorStatusOnSpan := func(s sdktrace.ReadOnlySpan) {
			assert.Equal(t, otelcodes.Error, s.Status().Code, "expected span status to be an error")
		}

		tp := sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exp),
			sdktrace.WithSpanProcessor(newOtelFakeSpanProcessor(checkErrorStatusOnSpan)),
		)
		oldTracerProvider := otel.GetTracerProvider()
		defer func() {
			_ = tp.Shutdown(context.Background())
			// reset tracer provider to older one once the test completes
			otel.SetTracerProvider(oldTracerProvider)
		}()
		otel.SetTracerProvider(tp)

		fakeInfo := &grpc.UnaryServerInfo{
			FullMethod: "/dapr.proto.runtime.v1.Dapr/InvokeService",
		}
		fakeReq := &runtimev1pb.InvokeServiceRequest{
			Id:      "targetID",
			Message: &commonv1pb.InvokeRequest{Method: "method1"},
		}

		var span trace.Span
		assertHandler := func(ctx context.Context, req any) (any, error) {
			span = diagUtils.SpanFromContext(ctx)
			// mocking an error that is returned from the gRPC API -- see pkg/grpc/api.go file
			return nil, status.Error(codes.Internal, errors.New("fake status error").Error())
		}

		interceptor(context.Background(), fakeReq, fakeInfo, assertHandler)

		sc := span.SpanContext()
		spanString := fmt.Sprintf("%v", span)
		assert.True(t, strings.Contains(spanString, "CallLocal/targetID/method1"))
		traceID := sc.TraceID()
		spanID := sc.SpanID()
		assert.NotEmpty(t, hex.EncodeToString(traceID[:]))
		assert.NotEmpty(t, hex.EncodeToString(spanID[:]))
	})
}

func TestGRPCTraceStreamServerInterceptor(t *testing.T) {
	exp := newOtelFakeExporter()

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
	)
	defer func() { _ = tp.Shutdown(context.Background()) }()
	otel.SetTracerProvider(tp)

	interceptor := GRPCTraceStreamServerInterceptor("test", config.TracingSpec{SamplingRate: "1"})

	testTraceParent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	testSpanContext, _ := SpanContextFromW3CString(testTraceParent)
	testTraceBinary := diagUtils.BinaryFromSpanContext(testSpanContext)

	t.Run("dapr runtime calls", func(t *testing.T) {
		t.Run("base test", func(t *testing.T) {
			fakeInfo := &grpc.StreamServerInfo{
				FullMethod: "/dapr.proto.runtime.v1.Dapr/GetState",
			}

			h := func(srv any, stream grpc.ServerStream) error {
				return nil
			}

			err := interceptor(nil, &fakeStream{}, fakeInfo, h)
			require.NoError(t, err)
		})

		t.Run("grpc-trace-bin is given", func(t *testing.T) {
			fakeInfo := &grpc.StreamServerInfo{
				FullMethod: "/dapr.proto.runtime.v1.Dapr/GetState",
			}

			ctx := grpcMetadata.NewIncomingContext(context.Background(), grpcMetadata.Pairs("grpc-trace-bin", string(testTraceBinary)))
			ctx, _ = metadata.SetMetadataInTapHandle(ctx, nil)

			var span trace.Span
			assertHandler := func(srv any, stream grpc.ServerStream) error {
				span = diagUtils.SpanFromContext(stream.Context())
				return errors.New("fake error")
			}

			interceptor(nil, &fakeStream{ctx}, fakeInfo, assertHandler)

			sc := span.SpanContext()
			traceID := sc.TraceID()
			assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", hex.EncodeToString(traceID[:]))
			spanID := sc.SpanID()
			assert.NotEqual(t, "00f067aa0ba902b7", hex.EncodeToString(spanID[:]))
		})

		t.Run("grpc-trace-bin is not given", func(t *testing.T) {
			fakeInfo := &grpc.StreamServerInfo{
				FullMethod: "/dapr.proto.runtime.v1.Dapr/GetState",
			}

			var span trace.Span
			assertHandler := func(srv any, stream grpc.ServerStream) error {
				span = diagUtils.SpanFromContext(stream.Context())
				return errors.New("fake error")
			}

			interceptor(nil, &fakeStream{}, fakeInfo, assertHandler)

			sc := span.SpanContext()
			traceID := sc.TraceID()
			spanID := sc.SpanID()
			assert.NotEmpty(t, hex.EncodeToString(traceID[:]))
			assert.NotEmpty(t, hex.EncodeToString(spanID[:]))
		})
	})

	t.Run("internal calls", func(t *testing.T) {
		t.Run("base test", func(t *testing.T) {
			fakeInfo := &grpc.StreamServerInfo{
				FullMethod: "/dapr.proto.internals.v1.ServiceInvocation/CallLocal",
			}

			h := func(srv any, stream grpc.ServerStream) error {
				return nil
			}

			err := interceptor(nil, &fakeStream{}, fakeInfo, h)
			require.NoError(t, err)
		})

		t.Run("grpc-trace-bin is given", func(t *testing.T) {
			fakeInfo := &grpc.StreamServerInfo{
				FullMethod: "/dapr.proto.internals.v1.ServiceInvocation/CallLocal",
			}

			ctx := grpcMetadata.NewIncomingContext(context.Background(), grpcMetadata.Pairs("grpc-trace-bin", string(testTraceBinary)))
			ctx, _ = metadata.SetMetadataInTapHandle(ctx, nil)

			var span trace.Span
			assertHandler := func(srv any, stream grpc.ServerStream) error {
				span = diagUtils.SpanFromContext(stream.Context())
				return errors.New("fake error")
			}

			interceptor(nil, &fakeStream{ctx}, fakeInfo, assertHandler)

			sc := span.SpanContext()
			traceID := sc.TraceID()
			assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", hex.EncodeToString(traceID[:]))
			spanID := sc.SpanID()
			assert.NotEqual(t, "00f067aa0ba902b7", hex.EncodeToString(spanID[:]))
		})

		t.Run("grpc-trace-bin is not given", func(t *testing.T) {
			fakeInfo := &grpc.StreamServerInfo{
				FullMethod: "/dapr.proto.internals.v1.ServiceInvocation/CallLocal",
			}

			var span trace.Span
			assertHandler := func(srv any, stream grpc.ServerStream) error {
				span = diagUtils.SpanFromContext(stream.Context())
				return errors.New("fake error")
			}

			interceptor(nil, &fakeStream{}, fakeInfo, assertHandler)

			sc := span.SpanContext()
			traceID := sc.TraceID()
			spanID := sc.SpanID()
			assert.NotEmpty(t, hex.EncodeToString(traceID[:]))
			assert.NotEmpty(t, hex.EncodeToString(spanID[:]))
		})
	})

	t.Run("proxy requests", func(t *testing.T) {
		t.Run("proxy request without app id, return error", func(t *testing.T) {
			fakeInfo := &grpc.StreamServerInfo{
				FullMethod: "/myapp.v1.DoSomething",
			}

			err := interceptor(nil, &fakeStream{}, fakeInfo, nil)
			require.Error(t, err)
		})

		t.Run("proxy request with app id and grpc-trace-bin", func(t *testing.T) {
			fakeInfo := &grpc.StreamServerInfo{
				FullMethod: "/myapp.v1.DoSomething",
			}

			md := grpcMetadata.New(map[string]string{
				GRPCProxyAppIDKey: "myapp",
				"grpc-trace-bin":  string(testTraceBinary),
			})
			ctx := grpcMetadata.NewIncomingContext(context.Background(), md)
			ctx, _ = metadata.SetMetadataInTapHandle(ctx, nil)

			var span trace.Span
			assertHandler := func(srv any, stream grpc.ServerStream) error {
				span = diagUtils.SpanFromContext(stream.Context())
				return nil
			}

			err := interceptor(nil, &fakeStream{ctx}, fakeInfo, assertHandler)
			require.NoError(t, err)

			sc := span.SpanContext()
			traceID := sc.TraceID()
			assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", hex.EncodeToString(traceID[:]))
			spanID := sc.SpanID()
			assert.NotEqual(t, "00f067aa0ba902b7", hex.EncodeToString(spanID[:]))
		})

		t.Run("proxy request with app id and no grpc-trace-bin", func(t *testing.T) {
			fakeInfo := &grpc.StreamServerInfo{
				FullMethod: "/myapp.v1.DoSomething",
			}

			md := grpcMetadata.New(map[string]string{
				GRPCProxyAppIDKey: "myapp",
			})
			ctx := grpcMetadata.NewIncomingContext(context.Background(), md)
			ctx, _ = metadata.SetMetadataInTapHandle(ctx, nil)

			var span trace.Span
			assertHandler := func(srv any, stream grpc.ServerStream) error {
				span = diagUtils.SpanFromContext(stream.Context())
				return nil
			}

			err := interceptor(nil, &fakeStream{ctx}, fakeInfo, assertHandler)
			require.NoError(t, err)

			sc := span.SpanContext()
			traceID := sc.TraceID()
			spanID := sc.SpanID()
			assert.NotEmpty(t, hex.EncodeToString(traceID[:]))
			assert.NotEmpty(t, hex.EncodeToString(spanID[:]))
		})
	})
}

type fakeStream struct {
	ctx context.Context
}

func (f *fakeStream) Context() context.Context {
	if f.ctx == nil {
		return context.Background()
	}
	return f.ctx
}

func (f *fakeStream) SetHeader(grpcMetadata.MD) error {
	return nil
}

func (f *fakeStream) SendHeader(grpcMetadata.MD) error {
	return nil
}

func (f *fakeStream) SetTrailer(grpcMetadata.MD) {
}

func (f *fakeStream) SendMsg(m any) error {
	return nil
}

func (f *fakeStream) RecvMsg(m any) error {
	return nil
}

func TestSpanContextSerialization(t *testing.T) {
	wantScConfig := trace.SpanContextConfig{
		TraceID:    trace.TraceID{75, 249, 47, 53, 119, 179, 77, 166, 163, 206, 146, 157, 14, 14, 71, 54},
		SpanID:     trace.SpanID{0, 240, 103, 170, 11, 169, 2, 183},
		TraceFlags: trace.TraceFlags(1),
	}
	wantSc := trace.NewSpanContext(wantScConfig)
	passedOverWire := diagUtils.BinaryFromSpanContext(wantSc)
	storedInDapr := base64.StdEncoding.EncodeToString(passedOverWire)
	decoded, _ := base64.StdEncoding.DecodeString(storedInDapr)
	gotSc, _ := diagUtils.SpanContextFromBinary(decoded)
	assert.Equal(t, wantSc, gotSc)
}
