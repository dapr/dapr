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
	otelbaggage "go.opentelemetry.io/otel/baggage"
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

	testCtx := grpcMetadata.NewIncomingContext(t.Context(), md)
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
		ctx := t.Context()
		newCtx := SpanContextToGRPCMetadata(ctx, trace.SpanContext{})

		assert.Equal(t, ctx, newCtx)
	})
}

// runBaggageHeaderPropagationTest runs the same baggage tests across both types of interceptors
func runBaggageHeaderPropagationTest(t *testing.T, interceptor interface{}) {
	// handle both types of interceptors
	var runInterceptor func(ctx context.Context) (context.Context, error)

	fakeInfo := &grpc.UnaryServerInfo{
		FullMethod: "/dapr.proto.runtime.v1.Dapr/GetState",
	}
	fakeReq := &runtimev1pb.GetStateRequest{
		StoreName: "statestore",
		Key:       "state",
	}

	switch intercept := interceptor.(type) {
	case grpc.UnaryServerInterceptor:
		runInterceptor = func(ctx context.Context) (context.Context, error) {
			var handlerCtx context.Context
			assertHandler := func(ctx context.Context, req any) (any, error) {
				handlerCtx = ctx
				return nil, nil
			}

			_, err := intercept(ctx, fakeReq, fakeInfo, assertHandler)
			return handlerCtx, err
		}
	case grpc.StreamServerInterceptor:
		streamInfo := &grpc.StreamServerInfo{
			FullMethod: fakeInfo.FullMethod,
		}

		runInterceptor = func(ctx context.Context) (context.Context, error) {
			var handlerCtx context.Context
			fakeStream := &fakeStream{ctx: ctx}

			streamHandler := func(srv interface{}, stream grpc.ServerStream) error {
				handlerCtx = stream.Context()
				return nil
			}

			err := intercept(nil, fakeStream, streamInfo, streamHandler)
			return handlerCtx, err
		}

	default:
		t.Fatalf("Unsupported interceptor type %T", interceptor)
	}

	t.Run("baggage from context only", func(t *testing.T) {
		bag, err := otelbaggage.Parse("key1=value1")
		require.NoError(t, err)
		ctx := otelbaggage.ContextWithBaggage(t.Context(), bag)

		handlerCtx, err := runInterceptor(ctx)
		require.NoError(t, err)

		baggage := otelbaggage.FromContext(handlerCtx)
		assert.NotNil(t, baggage)
		member := baggage.Member("key1")
		assert.Equal(t, "value1", member.Value())

		// Verify baggage is NOT propagated in response metadata
		incomingMd, ok := grpcMetadata.FromIncomingContext(handlerCtx)
		require.True(t, ok)
		baggageValues := incomingMd.Get("baggage")
		require.Empty(t, baggageValues)
	})

	t.Run("baggage from metadata only", func(t *testing.T) {
		md := grpcMetadata.New(map[string]string{
			"baggage": "key1=value1",
		})
		ctx := grpcMetadata.NewIncomingContext(t.Context(), md)

		handlerCtx, err := runInterceptor(ctx)
		require.NoError(t, err)

		// Verify metadata baggage is NOT propagated in the context
		baggage := otelbaggage.FromContext(handlerCtx)
		assert.Nil(t, baggage.Members())

		// Verify baggage header is set in response metadata
		incomingMd, ok := grpcMetadata.FromIncomingContext(handlerCtx)
		require.True(t, ok)
		baggageValues := incomingMd.Get("baggage")
		require.NotEmpty(t, baggageValues, "Expected baggage header to be set in metadata")
		assert.Equal(t, "key1=value1", baggageValues[0])
	})

	t.Run("baggage from both context and metadata", func(t *testing.T) {
		bag, err := otelbaggage.Parse("key1=value1")
		require.NoError(t, err)
		ctx := otelbaggage.ContextWithBaggage(t.Context(), bag)

		md := grpcMetadata.New(map[string]string{
			"baggage": "key2=value2",
		})
		ctx = grpcMetadata.NewIncomingContext(ctx, md)

		handlerCtx, err := runInterceptor(ctx)
		require.NoError(t, err)

		baggage := otelbaggage.FromContext(handlerCtx)
		assert.NotNil(t, baggage)

		// Verify key1 from context
		member := baggage.Member("key1")
		assert.Equal(t, "value1", member.Value())

		// Do NOT merge values
		assert.Empty(t, baggage.Member("key2").Value())

		// Verify key2 from metadata
		incomingMd, ok := grpcMetadata.FromIncomingContext(handlerCtx)
		require.True(t, ok)
		baggageValues := incomingMd.Get("baggage")
		require.NotEmpty(t, baggageValues, "Expected baggage header to be set in metadata")
		assert.Contains(t, baggageValues[0], "key2=value2")
	})

	t.Run("baggage header propagation with same kv", func(t *testing.T) {
		bag, err := otelbaggage.Parse("key1=value1")
		require.NoError(t, err)
		ctx := otelbaggage.ContextWithBaggage(t.Context(), bag)

		md := grpcMetadata.New(map[string]string{
			"baggage": "key1=value1",
		})
		ctx = grpcMetadata.NewIncomingContext(ctx, md)

		handlerCtx, err := runInterceptor(ctx)
		require.NoError(t, err)

		// Verify baggage is in the context
		baggage := otelbaggage.FromContext(handlerCtx)
		assert.NotNil(t, baggage)
		member := baggage.Member("key1")
		assert.Equal(t, "value1", member.Value())

		// Verify baggage header is set in incoming metadata
		incomingMd, ok := grpcMetadata.FromIncomingContext(handlerCtx)
		require.True(t, ok)
		baggageValues := incomingMd.Get("baggage")
		require.NotEmpty(t, baggageValues, "Expected baggage header to be set in metadata")
		assert.Equal(t, "key1=value1", baggageValues[0])
	})

	t.Run("multiple baggage values in context", func(t *testing.T) {
		bag, err := otelbaggage.Parse("key1=value1,key2=value2,key3=value3")
		require.NoError(t, err)
		ctx := otelbaggage.ContextWithBaggage(t.Context(), bag)

		handlerCtx, err := runInterceptor(ctx)
		require.NoError(t, err)

		// Verify all baggage items are in the context
		baggage := otelbaggage.FromContext(handlerCtx)
		assert.NotNil(t, baggage)
		assert.Equal(t, "value1", baggage.Member("key1").Value())
		assert.Equal(t, "value2", baggage.Member("key2").Value())
		assert.Equal(t, "value3", baggage.Member("key3").Value())

		// Verify baggage is NOT propagated in response metadata
		incomingMd, _ := metadata.FromIncomingContext(handlerCtx)
		baggageValues := incomingMd.Get("baggage")
		require.Empty(t, baggageValues)
	})

	t.Run("multiple baggage values in metadata", func(t *testing.T) {
		ctx := grpcMetadata.NewIncomingContext(t.Context(), grpcMetadata.New(map[string]string{
			"baggage": "key1=value1,key2=value2,key3=value3",
		}))

		handlerCtx, err := runInterceptor(ctx)
		require.NoError(t, err)

		// Verify metadata baggage is NOT propagated in the context
		baggage := otelbaggage.FromContext(handlerCtx)
		assert.Empty(t, baggage.Members())

		// Verify baggage header is set in response metadata
		incomingMd, _ := grpcMetadata.FromIncomingContext(handlerCtx)
		baggageValues := incomingMd.Get("baggage")
		require.NotEmpty(t, baggageValues, "Expected baggage header to be set in metadata")
		assert.Equal(t, "key1=value1,key2=value2,key3=value3", baggageValues[0])
	})

	t.Run("multiple different baggage values in both context and metadata", func(t *testing.T) {
		bag1, err := otelbaggage.Parse("ctx1=val1,ctx2=val2")
		require.NoError(t, err)
		ctx := otelbaggage.ContextWithBaggage(t.Context(), bag1)

		ctx = grpcMetadata.NewIncomingContext(ctx, grpcMetadata.New(map[string]string{
			"baggage": "meta1=val1,meta2=val2",
		}))

		handlerCtx, err := runInterceptor(ctx)
		require.NoError(t, err)

		baggage := otelbaggage.FromContext(handlerCtx)
		assert.NotNil(t, baggage)
		assert.Equal(t, "val1", baggage.Member("ctx1").Value())
		assert.Equal(t, "val2", baggage.Member("ctx2").Value())
		// Metadata baggage should not be in context
		assert.Empty(t, baggage.Member("meta1").Value())
		assert.Empty(t, baggage.Member("meta2").Value())

		incomingMd, _ := grpcMetadata.FromIncomingContext(handlerCtx)
		baggageValues := incomingMd.Get("baggage")
		require.NotEmpty(t, baggageValues, "Expected baggage header to be set in metadata")
		assert.Equal(t, "meta1=val1,meta2=val2", baggageValues[0])
	})

	t.Run("empty ctx baggage propagates", func(t *testing.T) {
		bag, err := otelbaggage.Parse("")
		require.NoError(t, err)
		ctx := otelbaggage.ContextWithBaggage(t.Context(), bag)

		handlerCtx, err := runInterceptor(ctx)
		require.NoError(t, err)

		// Verify baggage is in the ctx (OpenTelemetry creates empty baggage)
		baggage := otelbaggage.FromContext(handlerCtx)
		assert.NotNil(t, baggage, "baggage should be in context")
		assert.Empty(t, baggage.Members(), "baggage should be empty")
	})

	t.Run("empty metadata baggage propagates", func(t *testing.T) {
		md := grpcMetadata.New(map[string]string{
			"baggage": "",
		})
		ctx := grpcMetadata.NewIncomingContext(t.Context(), md)

		handlerCtx, err := runInterceptor(ctx)
		require.NoError(t, err)

		// Verify empty baggage is preserved in metadata
		md, ok := grpcMetadata.FromIncomingContext(handlerCtx)
		require.True(t, ok)
		baggageValues, exists := md["baggage"]
		require.True(t, exists, "baggage header should exist")
		assert.Equal(t, []string{""}, baggageValues, "empty baggage should be preserved")
	})

	t.Run("metadata baggage with properties", func(t *testing.T) {
		ctx := grpcMetadata.NewIncomingContext(t.Context(), grpcMetadata.Pairs(
			"baggage", "key1=value1;prop1=propvalue1,key2=value2;prop2=propvalue2",
		))

		handlerCtx, err := runInterceptor(ctx)
		require.NoError(t, err)

		// Verify baggage is preserved in metadata
		md, ok := grpcMetadata.FromIncomingContext(handlerCtx)
		require.True(t, ok)
		assert.Equal(t, "key1=value1;prop1=propvalue1,key2=value2;prop2=propvalue2", md.Get("baggage")[0])

		baggage := otelbaggage.FromContext(handlerCtx)
		assert.Nil(t, baggage.Members())
	})

	t.Run("context baggage with properties", func(t *testing.T) {
		bag, err := otelbaggage.Parse("key1=value1;prop1=propvalue1,key2=value2;prop2=propvalue2")
		require.NoError(t, err)
		assert.NotEmpty(t, bag)
		ctx := otelbaggage.ContextWithBaggage(t.Context(), bag)

		handlerCtx, err := runInterceptor(ctx)
		require.NoError(t, err)

		// Verify baggage is in ctx with both members & properties
		baggage := otelbaggage.FromContext(handlerCtx)
		assert.NotNil(t, baggage, "baggage should be in ctx")
		assert.Len(t, baggage.Members(), 2, "should have 2 baggage members")

		// key1
		member := baggage.Member("key1")
		assert.Equal(t, "value1", member.Value())
		props := member.Properties()
		assert.Len(t, props, 1, "should have one property")
		assert.Equal(t, "prop1", props[0].Key())
		propValue, exists := props[0].Value()
		assert.True(t, exists, "property should exist")
		assert.Equal(t, "propvalue1", propValue)
		// key2
		member = baggage.Member("key2")
		assert.Equal(t, "value2", member.Value())
		props = member.Properties()
		assert.Len(t, props, 1, "should have one property")
		assert.Equal(t, "prop2", props[0].Key())
		propValue, exists = props[0].Value()
		assert.True(t, exists, "property should exist")
		assert.Equal(t, "propvalue2", propValue)
	})

	t.Run("metadata baggage with special characters", func(t *testing.T) {
		ctx := grpcMetadata.NewIncomingContext(t.Context(), grpcMetadata.Pairs(
			"baggage", "key1=value1%20with%20spaces,key2=value2%2Fwith%2Fslashes",
		))

		handlerCtx, err := runInterceptor(ctx)
		require.NoError(t, err)

		// Verify baggage is preserved in metadata with percent-encoded values per W3C & for transport
		md, ok := grpcMetadata.FromIncomingContext(handlerCtx)
		require.True(t, ok)
		assert.Equal(t, "key1=value1%20with%20spaces,key2=value2%2Fwith%2Fslashes", md.Get("baggage")[0])
	})

	t.Run("context baggage with special characters", func(t *testing.T) {
		bag, err := otelbaggage.Parse("key1=value1%20with%20spaces,key2=value2%2Fwith%2Fslashes")
		require.NoError(t, err)
		assert.NotEmpty(t, bag)
		ctx := otelbaggage.ContextWithBaggage(t.Context(), bag)

		handlerCtx, err := runInterceptor(ctx)
		require.NoError(t, err)

		// Verify baggage is in ctx with both members (URL-decoded by OpenTelemetry)
		baggage := otelbaggage.FromContext(handlerCtx)
		assert.NotNil(t, baggage, "baggage should be in context")
		assert.Len(t, baggage.Members(), 2, "should have two baggage members")
		member := baggage.Member("key1")
		assert.Equal(t, "value1 with spaces", member.Value())
		member = baggage.Member("key2")
		assert.Equal(t, "value2/with/slashes", member.Value())
	})

	t.Run("invalid metadata baggage format", func(t *testing.T) {
		ctx := grpcMetadata.NewIncomingContext(t.Context(), grpcMetadata.Pairs(
			"baggage", "invalid-baggage",
		))

		_, err := runInterceptor(ctx)
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err), "invalid baggage should be rejected")
	})

	t.Run("multiple metadata baggage values in header", func(t *testing.T) {
		// single baggage header with multiple values
		ctx := grpcMetadata.NewIncomingContext(t.Context(), grpcMetadata.Pairs(
			"baggage", "key1=value1,key2=value2",
		))

		handlerCtx, err := runInterceptor(ctx)
		require.NoError(t, err)

		// baggage headers are combined in metadata
		md, ok := grpcMetadata.FromIncomingContext(handlerCtx)
		require.True(t, ok)
		bag := md.Get("baggage")
		require.NotEmpty(t, bag)
		assert.Equal(t, "key1=value1,key2=value2", bag[0])
	})

	t.Run("multiple context baggage values in header", func(t *testing.T) {
		bag, err := otelbaggage.Parse("key1=value1,key2=value2")
		require.NoError(t, err)
		assert.NotEmpty(t, bag)
		ctx := otelbaggage.ContextWithBaggage(t.Context(), bag)

		handlerCtx, err := runInterceptor(ctx)
		require.NoError(t, err)

		baggage := otelbaggage.FromContext(handlerCtx)
		assert.NotNil(t, baggage)
		member := baggage.Member("key1")
		assert.Equal(t, "value1", member.Value())
		member = baggage.Member("key2")
		assert.Equal(t, "value2", member.Value())

		// Verify metadata baggage is NOT present
		md, ok := grpcMetadata.FromIncomingContext(handlerCtx)
		require.True(t, ok)
		assert.Empty(t, md.Get("baggage"))
	})

	t.Run("mixed valid and invalid metadata baggage items", func(t *testing.T) {
		ctx := grpcMetadata.NewIncomingContext(t.Context(), grpcMetadata.Pairs(
			"baggage", "key1=value1,invalid-format-no-equals,key2=value2",
		))

		_, err := runInterceptor(ctx)
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err), "invalid baggage should be rejected")
	})

	t.Run("baggage at max item length", func(t *testing.T) {
		// Create ctx with baggage header at exactly max per-member len. Member, aka, item: k=v
		// "key1=value1,key2=" is 17 bytes, so we need MaxBaggageBytesPerMember-17 bytes of 'x's
		existingBaggageByteCount := len("key1=value1,key2=")
		maxBaggageBytesPerMember := 4096 // OpenTelemetry limit: https://github.com/open-telemetry/opentelemetry-go/blob/main/baggage/baggage.go
		longValue := strings.Repeat("x", maxBaggageBytesPerMember-existingBaggageByteCount)
		ctx := grpcMetadata.NewIncomingContext(t.Context(), grpcMetadata.Pairs(
			"baggage", "key1=value1,key2="+longValue,
		))

		handlerCtx, err := runInterceptor(ctx)
		require.NoError(t, err)

		baggage := otelbaggage.FromContext(handlerCtx)
		assert.Empty(t, baggage.Members())

		md, ok := grpcMetadata.FromIncomingContext(handlerCtx)
		require.True(t, ok)
		bag := md.Get("baggage")
		require.NotEmpty(t, bag)
		assert.Equal(t, "key1=value1,key2="+longValue, bag[0])
	})

	t.Run("baggage exceeding max item length", func(t *testing.T) {
		// MaxBaggageLength is the maximum length of a baggage header according to W3C spec
		// Reverence: https://www.w3.org/TR/baggage/#limits
		maxBagLen := 8192
		longValue := strings.Repeat("x", maxBagLen)
		ctx := grpcMetadata.NewIncomingContext(t.Context(), grpcMetadata.Pairs(
			"baggage", "key1=value1,key2="+longValue,
		))

		_, err := runInterceptor(ctx)
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err), "invalid baggage should be rejected")
	})
}

func TestGRPCTraceUnaryServerInterceptor(t *testing.T) {
	exp := newOtelFakeExporter()

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
	)
	defer func() { _ = tp.Shutdown(t.Context()) }()
	otel.SetTracerProvider(tp)

	interceptor := GRPCTraceUnaryServerInterceptor("fakeAppID", config.TracingSpec{SamplingRate: "1"})
	runBaggageHeaderPropagationTest(t, interceptor)

	testTraceParent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	testSpanContext, _ := SpanContextFromW3CString(testTraceParent)
	testTraceBinary := diagUtils.BinaryFromSpanContext(testSpanContext)

	t.Run("grpc-trace-bin is given", func(t *testing.T) {
		ctx := grpcMetadata.NewIncomingContext(t.Context(), grpcMetadata.Pairs("grpc-trace-bin", string(testTraceBinary)))
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

		interceptor(t.Context(), fakeReq, fakeInfo, assertHandler)

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

		interceptor(t.Context(), fakeReq, fakeInfo, assertHandler)

		sc := span.SpanContext()
		spanString := fmt.Sprintf("%v", span)
		assert.Contains(t, spanString, "CallLocal/targetID/method1")
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
			_ = tp.Shutdown(t.Context())
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

		interceptor(t.Context(), fakeReq, fakeInfo, assertHandler)

		sc := span.SpanContext()
		spanString := fmt.Sprintf("%v", span)
		assert.Contains(t, spanString, "CallLocal/targetID/method1")
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
	defer func() { _ = tp.Shutdown(t.Context()) }()
	otel.SetTracerProvider(tp)

	interceptor := GRPCTraceStreamServerInterceptor("test", config.TracingSpec{SamplingRate: "1"})
	runBaggageHeaderPropagationTest(t, interceptor)

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

			ctx := grpcMetadata.NewIncomingContext(t.Context(), grpcMetadata.Pairs("grpc-trace-bin", string(testTraceBinary)))
			ctx, _ = metadata.SetMetadataInTapHandle(ctx, nil)

			var span trace.Span
			assertHandler := func(srv any, stream grpc.ServerStream) error {
				span = diagUtils.SpanFromContext(stream.Context())
				return errors.New("fake error")
			}

			interceptor(nil, &fakeStream{ctx: ctx, header: grpcMetadata.MD{}, trailer: grpcMetadata.MD{}}, fakeInfo, assertHandler)

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

			ctx := grpcMetadata.NewIncomingContext(t.Context(), grpcMetadata.Pairs("grpc-trace-bin", string(testTraceBinary)))
			ctx, _ = metadata.SetMetadataInTapHandle(ctx, nil)

			var span trace.Span
			assertHandler := func(srv any, stream grpc.ServerStream) error {
				span = diagUtils.SpanFromContext(stream.Context())
				return errors.New("fake error")
			}

			interceptor(nil, &fakeStream{ctx: ctx, header: grpcMetadata.MD{}, trailer: grpcMetadata.MD{}}, fakeInfo, assertHandler)

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
				"dapr-app-id":    "myapp",
				"grpc-trace-bin": string(testTraceBinary),
			})
			ctx := grpcMetadata.NewIncomingContext(t.Context(), md)
			ctx, _ = metadata.SetMetadataInTapHandle(ctx, nil)

			var span trace.Span
			assertHandler := func(srv any, stream grpc.ServerStream) error {
				span = diagUtils.SpanFromContext(stream.Context())
				return nil
			}

			err := interceptor(nil, &fakeStream{ctx: ctx, header: grpcMetadata.MD{}, trailer: grpcMetadata.MD{}}, fakeInfo, assertHandler)
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
				"dapr-app-id": "myapp",
			})
			ctx := grpcMetadata.NewIncomingContext(t.Context(), md)
			ctx, _ = metadata.SetMetadataInTapHandle(ctx, nil)

			var span trace.Span
			assertHandler := func(srv any, stream grpc.ServerStream) error {
				span = diagUtils.SpanFromContext(stream.Context())
				return nil
			}

			err := interceptor(nil, &fakeStream{ctx: ctx, header: grpcMetadata.MD{}, trailer: grpcMetadata.MD{}}, fakeInfo, assertHandler)
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
	ctx     context.Context
	header  metadata.MD
	trailer metadata.MD
}

func (f *fakeStream) Context() context.Context {
	if f.ctx == nil {
		return context.Background()
	}
	return f.ctx
}

func (f *fakeStream) SetHeader(md grpcMetadata.MD) error {
	f.header = md
	return nil
}

func (f *fakeStream) SendHeader(md grpcMetadata.MD) error {
	f.header = md
	return nil
}

func (f *fakeStream) SetTrailer(md grpcMetadata.MD) {
	f.trailer = md
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
