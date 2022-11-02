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

package v1

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/trace"
	epb "google.golang.org/genproto/googleapis/rpc/errdetails"
	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	grpcStatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"

	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

const (
	// GRPCContentType is the MIME media type for grpc.
	GRPCContentType = "application/grpc"
	// JSONContentType is the MIME media type for JSON.
	JSONContentType = "application/json"
	// ProtobufContentType is the MIME media type for Protobuf.
	ProtobufContentType = "application/x-protobuf"

	// ContentTypeHeader is the header key of content-type.
	ContentTypeHeader = "content-type"
	// DaprHeaderPrefix is the prefix if metadata is defined by non user-defined http headers.
	DaprHeaderPrefix = "dapr-"

	// W3C trace correlation headers.
	traceparentHeader = "traceparent"
	tracestateHeader  = "tracestate"

	// DestinationIDHeader is the header carrying the value of the invoked app id.
	DestinationIDHeader = "destination-app-id"
	DaprAppIDKey        = "dapr-app-id"

	// ErrorInfo metadata value is limited to 64 chars
	// https://github.com/googleapis/googleapis/blob/master/google/rpc/error_details.proto#L126
	maxMetadataValueLen = 63

	// ErrorInfo metadata for HTTP response.
	errorInfoDomain            = "dapr.io"
	errorInfoHTTPCodeMetadata  = "http.code"
	errorInfoHTTPErrorMetadata = "http.error_message"
)

// DaprInternalMetadata is the metadata type to transfer HTTP header and gRPC metadata
// from user app to Dapr.
type DaprInternalMetadata map[string]*internalv1pb.ListStringValue

// MetadataToInternalMetadata converts metadata to dapr internal metadata map.
func MetadataToInternalMetadata(md map[string][]string) DaprInternalMetadata {
	internalMD := make(DaprInternalMetadata, len(md))
	for k, values := range md {
		listValue := internalv1pb.ListStringValue{}
		listValue.Values = append(listValue.Values, values...)
		internalMD[k] = &listValue
	}

	return internalMD
}

// isPermanentHTTPHeader checks whether hdr belongs to the list of
// permanent request headers maintained by IANA.
// http://www.iana.org/assignments/message-headers/message-headers.xml
func isPermanentHTTPHeader(hdr string) bool {
	switch hdr {
	case
		"Accept",
		"Accept-Charset",
		"Accept-Language",
		"Accept-Ranges",
		// Connection-specific header fields such as Connection and Keep-Alive are prohibited in HTTP/2.
		// See https://tools.ietf.org/html/rfc7540#section-8.1.2.2.
		"Connection",
		"Keep-Alive",
		"Proxy-Connection",
		"Transfer-Encoding",
		"Upgrade",
		"Cache-Control",
		"Content-Type",
		// Remove content-length header since it represents http1.1 payload size,
		// not the sum of the h2 DATA frame payload lengths.
		// See https://httpwg.org/specs/rfc7540.html#malformed.
		"Content-Length",
		"Cookie",
		"Date",
		"Expect",
		"From",
		"Host",
		"If-Match",
		"If-Modified-Since",
		"If-None-Match",
		"If-Schedule-Tag-Match",
		"If-Unmodified-Since",
		"Max-Forwards",
		"Origin",
		"Pragma",
		"Referer",
		"Via",
		"Warning":
		return true
	}
	return false
}

// InternalMetadataToGrpcMetadata converts internal metadata map to gRPC metadata.
func InternalMetadataToGrpcMetadata(ctx context.Context, internalMD DaprInternalMetadata, httpHeaderConversion bool) metadata.MD {
	var traceparentValue, tracestateValue string
	md := metadata.MD{}
	for k, listVal := range internalMD {
		keyName := strings.ToLower(k)
		// get both the trace headers for HTTP/GRPC and continue
		switch keyName {
		case traceparentHeader:
			traceparentValue = listVal.Values[0]
			continue
		case tracestateHeader:
			tracestateValue = listVal.Values[0]
			continue
		case DestinationIDHeader:
			continue
		}

		if httpHeaderConversion && isPermanentHTTPHeader(k) {
			keyName = strings.ToLower(DaprHeaderPrefix + keyName)
		}

		md.Append(keyName, listVal.Values...)
	}

	ProcessSpanContextToMetadata(ctx, &WrapMetadata{md}, traceparentValue, tracestateValue)

	return md
}

// IsGRPCProtocol checks if metadata is originated from gRPC API.
func IsGRPCProtocol(internalMD DaprInternalMetadata) bool {
	originContentType := ""
	if val, ok := internalMD[ContentTypeHeader]; ok {
		originContentType = val.Values[0]
	}
	return strings.HasPrefix(originContentType, GRPCContentType)
}

func ReservedGRPCMetadataToDaprPrefixHeader(key string) string {
	// https://github.com/grpc/grpc/blob/master/doc/PROTOCOL-HTTP2.md
	if key == ":method" || key == ":scheme" || key == ":path" || key == ":authority" {
		return DaprHeaderPrefix + key[1:]
	}
	if strings.HasPrefix(key, "grpc-") {
		return DaprHeaderPrefix + key
	}

	return key
}

// InternalMetadataToHTTPHeader converts internal metadata pb to HTTP headers.
func InternalMetadataToHTTPHeader(ctx context.Context, internalMD DaprInternalMetadata, setHeader func(string, string)) {
	var traceparentValue, tracestateValue string
	for k, listVal := range internalMD {
		keyName := strings.ToLower(k)
		// get both the trace headers for HTTP/GRPC and continue
		switch keyName {
		case traceparentHeader:
			traceparentValue = listVal.Values[0]
			continue
		case tracestateHeader:
			tracestateValue = listVal.Values[0]
			continue
		case DestinationIDHeader:
			continue
		}

		if len(listVal.Values) == 0 || keyName == ContentTypeHeader {
			continue
		}
		setHeader(ReservedGRPCMetadataToDaprPrefixHeader(keyName), listVal.Values[0])
	}

	ProcessSpanContextToMetadata(ctx, SetFunc(setHeader), traceparentValue, tracestateValue)
}

// HTTPStatusFromCode converts a gRPC error code into the corresponding HTTP response status.
// https://github.com/grpc-ecosystem/grpc-gateway/blob/master/runtime/errors.go#L15
// See: https://github.com/googleapis/googleapis/blob/master/google/rpc/code.proto
func HTTPStatusFromCode(code codes.Code) int {
	switch code {
	case codes.OK:
		return http.StatusOK
	case codes.Canceled:
		return http.StatusRequestTimeout
	case codes.Unknown:
		return http.StatusInternalServerError
	case codes.InvalidArgument:
		return http.StatusBadRequest
	case codes.DeadlineExceeded:
		return http.StatusGatewayTimeout
	case codes.NotFound:
		return http.StatusNotFound
	case codes.AlreadyExists:
		return http.StatusConflict
	case codes.PermissionDenied:
		return http.StatusForbidden
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests
	case codes.FailedPrecondition:
		// Note, this deliberately doesn't translate to the similarly named '412 Precondition Failed' HTTP response status.
		return http.StatusBadRequest
	case codes.Aborted:
		return http.StatusConflict
	case codes.OutOfRange:
		return http.StatusBadRequest
	case codes.Unimplemented:
		return http.StatusNotImplemented
	case codes.Internal:
		return http.StatusInternalServerError
	case codes.Unavailable:
		return http.StatusServiceUnavailable
	case codes.DataLoss:
		return http.StatusInternalServerError
	}

	return http.StatusInternalServerError
}

// CodeFromHTTPStatus converts http status code to gRPC status code
// See: https://github.com/grpc/grpc/blob/master/doc/http-grpc-status-mapping.md
func CodeFromHTTPStatus(httpStatusCode int) codes.Code {
	switch httpStatusCode {
	case http.StatusRequestTimeout:
		return codes.Canceled
	case http.StatusInternalServerError:
		return codes.Unknown
	case http.StatusBadRequest:
		return codes.Internal
	case http.StatusGatewayTimeout:
		return codes.DeadlineExceeded
	case http.StatusNotFound:
		return codes.NotFound
	case http.StatusConflict:
		return codes.AlreadyExists
	case http.StatusForbidden:
		return codes.PermissionDenied
	case http.StatusUnauthorized:
		return codes.Unauthenticated
	case http.StatusTooManyRequests:
		return codes.ResourceExhausted
	case http.StatusNotImplemented:
		return codes.Unimplemented
	case http.StatusServiceUnavailable:
		return codes.Unavailable
	}

	if httpStatusCode >= 200 && httpStatusCode < 300 {
		return codes.OK
	}

	return codes.Unknown
}

// ErrorFromHTTPResponseCode converts http response code to gRPC status error.
func ErrorFromHTTPResponseCode(code int, detail string) error {
	grpcCode := CodeFromHTTPStatus(code)
	if grpcCode == codes.OK {
		return nil
	}
	httpStatusText := http.StatusText(code)
	respStatus := grpcStatus.New(grpcCode, httpStatusText)

	// Truncate detail string longer than 64 characters
	if len(detail) >= maxMetadataValueLen {
		detail = detail[:maxMetadataValueLen]
	}

	resps, err := respStatus.WithDetails(
		&epb.ErrorInfo{
			Reason: httpStatusText,
			Domain: errorInfoDomain,
			Metadata: map[string]string{
				errorInfoHTTPCodeMetadata:  strconv.Itoa(code),
				errorInfoHTTPErrorMetadata: detail,
			},
		},
	)
	if err != nil {
		resps = respStatus
	}

	return resps.Err()
}

// ErrorFromInternalStatus converts internal status to gRPC status error.
func ErrorFromInternalStatus(internalStatus *internalv1pb.Status) error {
	respStatus := &spb.Status{
		Code:    internalStatus.GetCode(),
		Message: internalStatus.GetMessage(),
		Details: internalStatus.GetDetails(),
	}

	return grpcStatus.ErrorProto(respStatus)
}

// Seter set key-value interface, using metadata.MD and http header.
type Seter interface {
	Set(string, string)
}

// WrapMetadata wraps grpc metadata to using set key-value.
type WrapMetadata struct {
	md metadata.MD
}

func (w *WrapMetadata) Set(key string, value string) {
	w.md.Set(key, value)
}

type SetFunc func(string, string)

func (s SetFunc) Set(key string, value string) {
	s(key, value)
}

func ProcessSpanContextToMetadata(ctx context.Context, kvFunc Seter, traceparentValue, traceStateValue string) {
	var sc trace.SpanContext
	if sc = diagUtils.SpanContextFromW3CString(traceparentValue); sc.IsValid() {
		ts := diagUtils.TraceStateFromW3CString(traceStateValue)
		sc = sc.WithTraceState(ts)
	} else {
		sc = trace.SpanContextFromContext(ctx)
	}
	diagUtils.SpanContextToMetadata(sc, func(header, value string) {
		kvFunc.Set(header, value)
	})
}

// ProtobufToJSON serializes Protobuf message to json format.
func ProtobufToJSON(message protoreflect.ProtoMessage) ([]byte, error) {
	marshaler := protojson.MarshalOptions{
		Indent:          "",
		UseProtoNames:   false,
		EmitUnpopulated: false,
	}
	return marshaler.Marshal(message)
}

// WithCustomGRPCMetadata applies a metadata map to the outgoing context metadata.
func WithCustomGRPCMetadata(ctx context.Context, md map[string]string) context.Context {
	for k, v := range md {
		// Uppercase keys will be converted to lowercase.
		ctx = metadata.AppendToOutgoingContext(ctx, k, v)
	}

	return ctx
}
