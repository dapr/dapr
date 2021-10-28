// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package v1

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	epb "google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

func TestInternalMetadataToHTTPHeader(t *testing.T) {
	testValue := &internalv1pb.ListStringValue{
		Values: []string{"fakeValue"},
	}

	fakeMetadata := map[string]*internalv1pb.ListStringValue{
		"custom-header":  testValue,
		":method":        testValue,
		":scheme":        testValue,
		":path":          testValue,
		":authority":     testValue,
		"grpc-timeout":   testValue,
		"content-type":   testValue, // skip
		"grpc-trace-bin": testValue,
	}

	expectedKeyNames := []string{"custom-header", "dapr-method", "dapr-scheme", "dapr-path", "dapr-authority", "dapr-grpc-timeout"}
	savedHeaderKeyNames := []string{}
	ctx := context.Background()
	InternalMetadataToHTTPHeader(ctx, fakeMetadata, func(k, v string) {
		savedHeaderKeyNames = append(savedHeaderKeyNames, k)
	})

	sort.Strings(expectedKeyNames)
	sort.Strings(savedHeaderKeyNames)

	assert.Equal(t, expectedKeyNames, savedHeaderKeyNames)
}

func TestGrpcMetadataToInternalMetadata(t *testing.T) {
	keyBinValue := []byte{101, 200}
	testMD := metadata.Pairs(
		"key", "key value",
		"key-bin", string(keyBinValue),
	)
	internalMD := MetadataToInternalMetadata(testMD)

	assert.Equal(t, "key value", internalMD["key"].GetValues()[0])
	assert.Equal(t, 1, len(internalMD["key"].GetValues()))

	assert.Equal(t, base64.StdEncoding.EncodeToString(keyBinValue), internalMD["key-bin"].GetValues()[0], "binary metadata must be saved")
	assert.Equal(t, 1, len(internalMD["key-bin"].GetValues()))
}

func TestIsJSONContentType(t *testing.T) {
	contentTypeTests := []struct {
		in  string
		out bool
	}{
		{"application/json", true},
		{"text/plains; charset=utf-8", false},
		{"application/json; charset=utf-8", true},
	}

	for _, tt := range contentTypeTests {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.out, IsJSONContentType(tt.in))
		})
	}
}

func TestInternalMetadataToGrpcMetadata(t *testing.T) {
	httpHeaders := map[string]*internalv1pb.ListStringValue{
		"Host": {
			Values: []string{"localhost"},
		},
		"Content-Type": {
			Values: []string{"application/json"},
		},
		"Content-Length": {
			Values: []string{"2000"},
		},
		"Connection": {
			Values: []string{"keep-alive"},
		},
		"Keep-Alive": {
			Values: []string{"timeout=5", "max=1000"},
		},
		"Proxy-Connection": {
			Values: []string{"keep-alive"},
		},
		"Transfer-Encoding": {
			Values: []string{"gzip", "chunked"},
		},
		"Upgrade": {
			Values: []string{"WebSocket"},
		},
		"Accept-Encoding": {
			Values: []string{"gzip, deflate"},
		},
		"User-Agent": {
			Values: []string{"Go-http-client/1.1"},
		},
	}

	ctx := context.Background()

	t.Run("without http header conversion for http headers", func(t *testing.T) {
		convertedMD := InternalMetadataToGrpcMetadata(ctx, httpHeaders, false)
		// always trace header is returned
		assert.Equal(t, 11, convertedMD.Len())

		testHeaders := []struct {
			key      string
			expected string
		}{
			{"host", "localhost"},
			{"connection", "keep-alive"},
			{"content-length", "2000"},
			{"content-type", "application/json"},
			{"keep-alive", "timeout=5"},
			{"proxy-connection", "keep-alive"},
			{"transfer-encoding", "gzip"},
			{"upgrade", "WebSocket"},
			{"accept-encoding", "gzip, deflate"},
			{"user-agent", "Go-http-client/1.1"},
		}

		for _, ht := range testHeaders {
			assert.Equal(t, ht.expected, convertedMD[ht.key][0])
		}
	})

	t.Run("with http header conversion for http headers", func(t *testing.T) {
		convertedMD := InternalMetadataToGrpcMetadata(ctx, httpHeaders, true)
		// always trace header is returned
		assert.Equal(t, 11, convertedMD.Len())

		testHeaders := []struct {
			key      string
			expected string
		}{
			{"dapr-host", "localhost"},
			{"dapr-connection", "keep-alive"},
			{"dapr-content-length", "2000"},
			{"dapr-content-type", "application/json"},
			{"dapr-keep-alive", "timeout=5"},
			{"dapr-proxy-connection", "keep-alive"},
			{"dapr-transfer-encoding", "gzip"},
			{"dapr-upgrade", "WebSocket"},
			{"accept-encoding", "gzip, deflate"},
			{"user-agent", "Go-http-client/1.1"},
		}

		for _, ht := range testHeaders {
			assert.Equal(t, ht.expected, convertedMD[ht.key][0])
		}
	})

	keyBinValue := []byte{100, 50}
	keyBinEncodedValue := base64.StdEncoding.EncodeToString(keyBinValue)

	traceBinValue := []byte{10, 30, 50, 60}
	traceBinValueEncodedValue := base64.StdEncoding.EncodeToString(traceBinValue)

	grpcMetadata := map[string]*internalv1pb.ListStringValue{
		"content-type": {
			Values: []string{"application/grpc"},
		},
		":authority": {
			Values: []string{"localhost"},
		},
		"grpc-timeout": {
			Values: []string{"1S"},
		},
		"grpc-encoding": {
			Values: []string{"gzip, deflate"},
		},
		"authorization": {
			Values: []string{"bearer token"},
		},
		"grpc-trace-bin": {
			Values: []string{traceBinValueEncodedValue},
		},
		"my-metadata": {
			Values: []string{"value1", "value2", "value3"},
		},
		"key-bin": {
			Values: []string{keyBinEncodedValue, keyBinEncodedValue},
		},
	}

	t.Run("with grpc header conversion for grpc headers", func(t *testing.T) {
		convertedMD := InternalMetadataToGrpcMetadata(ctx, grpcMetadata, true)
		assert.Equal(t, 8, convertedMD.Len())
		assert.Equal(t, "localhost", convertedMD[":authority"][0])
		assert.Equal(t, "1S", convertedMD["grpc-timeout"][0])
		assert.Equal(t, "gzip, deflate", convertedMD["grpc-encoding"][0])
		assert.Equal(t, "bearer token", convertedMD["authorization"][0])
		_, ok := convertedMD["grpc-trace-bin"]
		assert.True(t, ok)
		assert.Equal(t, "value1", convertedMD["my-metadata"][0])
		assert.Equal(t, "value2", convertedMD["my-metadata"][1])
		assert.Equal(t, "value3", convertedMD["my-metadata"][2])
		assert.Equal(t, string(keyBinValue), convertedMD["key-bin"][0])
		assert.Equal(t, string(keyBinValue), convertedMD["key-bin"][1])
		assert.Equal(t, string(traceBinValue), convertedMD["grpc-trace-bin"][0])
	})
}

func TestErrorFromHTTPResponseCode(t *testing.T) {
	t.Run("OK", func(t *testing.T) {
		// act
		err := ErrorFromHTTPResponseCode(200, "OK")

		// assert
		assert.NoError(t, err)
	})

	t.Run("Created", func(t *testing.T) {
		// act
		err := ErrorFromHTTPResponseCode(201, "Created")

		// assert
		assert.NoError(t, err)
	})

	t.Run("NotFound", func(t *testing.T) {
		// act
		err := ErrorFromHTTPResponseCode(404, "Not Found")

		// assert
		s, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.NotFound, s.Code())
		assert.Equal(t, "Not Found", s.Message())
		errInfo := (s.Details()[0]).(*epb.ErrorInfo)
		assert.Equal(t, "404", errInfo.GetMetadata()[errorInfoHTTPCodeMetadata])
		assert.Equal(t, "Not Found", errInfo.GetMetadata()[errorInfoHTTPErrorMetadata])
	})

	t.Run("Internal Server Error", func(t *testing.T) {
		// act
		err := ErrorFromHTTPResponseCode(500, "HTTPExtensions is not given")

		// assert
		s, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.Unknown, s.Code())
		assert.Equal(t, "Internal Server Error", s.Message())
		errInfo := (s.Details()[0]).(*epb.ErrorInfo)
		assert.Equal(t, "500", errInfo.GetMetadata()[errorInfoHTTPCodeMetadata])
		assert.Equal(t, "HTTPExtensions is not given", errInfo.GetMetadata()[errorInfoHTTPErrorMetadata])
	})

	t.Run("Truncate error message", func(t *testing.T) {
		longMessage := strings.Repeat("test", 30)

		// act
		err := ErrorFromHTTPResponseCode(500, longMessage)

		// assert
		s, _ := status.FromError(err)
		errInfo := (s.Details()[0]).(*epb.ErrorInfo)
		assert.Equal(t, 63, len(errInfo.GetMetadata()[errorInfoHTTPErrorMetadata]))
	})
}

func TestErrorFromInternalStatus(t *testing.T) {
	expected := status.New(codes.Internal, "Internal Service Error")
	expected.WithDetails(
		&epb.DebugInfo{
			StackEntries: []string{
				"first stack",
				"second stack",
			},
		},
	)

	internal := &internalv1pb.Status{
		Code:    expected.Proto().Code,
		Message: expected.Proto().Message,
		Details: expected.Proto().Details,
	}

	expected.Message()

	// act
	statusError := ErrorFromInternalStatus(internal)

	// assert
	actual, ok := status.FromError(statusError)
	assert.True(t, ok)
	assert.Equal(t, expected.Code(), actual.Code())
	assert.Equal(t, expected.Message(), actual.Message())
	assert.Equal(t, expected.Details(), actual.Details())
}

func TestCloneBytes(t *testing.T) {
	t.Run("data is nil", func(t *testing.T) {
		assert.Nil(t, cloneBytes(nil))
	})

	t.Run("data is empty", func(t *testing.T) {
		orig := []byte{}

		assert.Equal(t, orig, cloneBytes(orig))
		assert.NotSame(t, orig, cloneBytes(orig))
	})

	t.Run("data is not empty", func(t *testing.T) {
		orig := []byte("fakedata")

		assert.Equal(t, orig, cloneBytes(orig))
		assert.NotSame(t, orig, cloneBytes(orig))
	})
}

func TestProtobufToJSON(t *testing.T) {
	tpb := &epb.DebugInfo{
		StackEntries: []string{
			"first stack",
			"second stack",
		},
	}

	jsonBody, err := ProtobufToJSON(tpb)
	assert.NoError(t, err)
	t.Log(string(jsonBody))

	// protojson produces different indentation space based on OS
	// For linux
	comp1 := string(jsonBody) == "{\"stackEntries\":[\"first stack\",\"second stack\"]}"
	// For mac and windows
	comp2 := string(jsonBody) == "{\"stackEntries\":[\"first stack\", \"second stack\"]}"
	assert.True(t, comp1 || comp2)
}

func TestWithCustomGrpcMetadata(t *testing.T) {
	customMetadataKey := func(i int) string {
		return fmt.Sprintf("customMetadataKey%d", i)
	}
	customMetadataValue := func(i int) string {
		return fmt.Sprintf("customMetadataValue%d", i)
	}

	numMetadata := 10
	md := make(map[string]string, numMetadata)
	for i := 0; i < numMetadata; i++ {
		md[customMetadataKey(i)] = customMetadataValue(i)
	}

	ctx := context.Background()
	ctx = WithCustomGRPCMetadata(ctx, md)

	ctxMd, ok := metadata.FromOutgoingContext(ctx)
	assert.True(t, ok)

	for i := 0; i < numMetadata; i++ {
		val, ok := ctxMd[strings.ToLower(customMetadataKey(i))]
		assert.True(t, ok)
		// We assume only 1 value per key as the input map can only support string -> string mapping.
		assert.Equal(t, customMetadataValue(i), val[0])
	}
}
