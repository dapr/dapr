// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package v1

import (
	"net/http"
	"strings"

	structpb "github.com/golang/protobuf/ptypes/struct"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
)

const (
	// GRPCContentType is the MIME media type for grpc
	GRPCContentType = "application/grpc"
	// JSONContentType is the MIME media type for JSON
	JSONContentType = "application/json"
	// ProtobufContentType is the MIME media type for Protobuf
	ProtobufContentType = "application/x-protobuf"

	// ContentTypeHeader is the header key of content-type
	ContentTypeHeader = "content-type"
	// DaprHeaderPrefix is the prefix if metadata is defined by non user-defined http headers
	DaprHeaderPrefix = "dapr-"
	// gRPCBinaryMetadata is the suffix of grpc metadata binary value
	gRPCBinaryMetadataSuffix = "-bin"

	// W3C trace correlation headers
	traceparentHeader = "traceparent"
	tracestateHeader  = "tracestate"
	tracebinMetadata  = "grpc-trace-bin"
)

// DaprInternalMetadata is the metadata type to transfer HTTP header and gRPC metadata
// from user app to Dapr.
type DaprInternalMetadata map[string]*structpb.ListValue

// IsJSONContentType returns true if contentType is the mime media type for JSON
func IsJSONContentType(contentType string) bool {
	return strings.HasPrefix(strings.ToLower(contentType), JSONContentType)
}

// GrpcMetadataToInternalMetadata converts gRPC metadata to dapr internal metadata map
func GrpcMetadataToInternalMetadata(md metadata.MD) DaprInternalMetadata {
	var internalMD = DaprInternalMetadata{}
	for k, values := range md {
		var listValue = structpb.ListValue{}
		for _, v := range values {
			listValue.Values = append(listValue.Values, &structpb.Value{
				Kind: &structpb.Value_StringValue{StringValue: v},
			})
		}
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
		// "Authorization",
		"Cache-Control",
		"Content-Type",
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
		// "User-Agent",
		"Via",
		"Warning":
		return true
	}
	return false
}

func isTraceCorrleationHeaderKey(key string) bool {
	k := strings.ToLower(key)
	return k == tracestateHeader || k == traceparentHeader || k == tracebinMetadata
}

// InternalMetadataToGrpcMetadata converts internal metadata map to gRPC metadata
func InternalMetadataToGrpcMetadata(internalMD DaprInternalMetadata, httpHeaderConversion bool) metadata.MD {
	var md = metadata.MD{}
	for k, listVal := range internalMD {
		if isTraceCorrleationHeaderKey(k) {
			continue
		}

		keyName := strings.ToLower(k)
		if httpHeaderConversion && isPermanentHTTPHeader(k) {
			keyName = strings.ToLower(DaprHeaderPrefix + keyName)
		}
		for _, v := range listVal.Values {
			md.Append(keyName, v.GetStringValue())
		}
	}
	return md
}

// IsGRPCProtocol checks if metadata is originated from gRPC API
func IsGRPCProtocol(internalMD DaprInternalMetadata) bool {
	var originContentType = ""
	if val, ok := internalMD[ContentTypeHeader]; ok {
		originContentType = val.Values[0].GetStringValue()
	}
	return strings.HasPrefix(originContentType, GRPCContentType)
}

func reservedGRPCMetadataToDaprPrefixHeader(key string) string {
	// https://github.com/grpc/grpc/blob/master/doc/PROTOCOL-HTTP2.md
	if key == ":method" || key == ":scheme" || key == ":path" || key == ":authority" {
		return DaprHeaderPrefix + key[1:]
	}
	if strings.HasPrefix(key, "grpc-") {
		return DaprHeaderPrefix + key
	}

	return key
}

// InternalMetadataToHTTPHeader converts internal metadata pb to HTTP headers
func InternalMetadataToHTTPHeader(internalMD DaprInternalMetadata, setHeader func(string, string)) {
	for k, listVal := range internalMD {
		// Skip if the header key has -bin suffix
		if len(listVal.Values) == 0 || strings.HasSuffix(k, gRPCBinaryMetadataSuffix) || k == ContentTypeHeader || isTraceCorrleationHeaderKey(k) {
			continue
		}
		setHeader(reservedGRPCMetadataToDaprPrefixHeader(k), listVal.Values[0].GetStringValue())
	}
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
	case http.StatusOK:
		return codes.OK
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

	return codes.Unknown
}
