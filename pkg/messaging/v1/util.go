// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package v1

import (
	"net/http"
	"strconv"
	"strings"

	diag "github.com/dapr/dapr/pkg/diagnostics"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"go.opencensus.io/trace/propagation"
	epb "google.golang.org/genproto/googleapis/rpc/errdetails"
	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	grpc_status "google.golang.org/grpc/status"
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

	// ErrorInfo metadata value is limited to 64 chars
	// https://github.com/googleapis/googleapis/blob/master/google/rpc/error_details.proto#L126
	maxMetadataValueLen = 63

	// ErrorInfo metadata for HTTP response
	errorInfoDomain            = "dapr.io"
	errorInfoHTTPCodeMetadata  = "http.code"
	errorInfoHTTPErrorMetadata = "http.error_message"
)

// DaprInternalMetadata is the metadata type to transfer HTTP header and gRPC metadata
// from user app to Dapr.
type DaprInternalMetadata map[string]*internalv1pb.ListStringValue

// IsJSONContentType returns true if contentType is the mime media type for JSON
func IsJSONContentType(contentType string) bool {
	return strings.HasPrefix(strings.ToLower(contentType), JSONContentType)
}

// GrpcMetadataToInternalMetadata converts gRPC metadata to dapr internal metadata map
func GrpcMetadataToInternalMetadata(md metadata.MD) DaprInternalMetadata {
	var internalMD = DaprInternalMetadata{}
	for k, values := range md {
		var listValue = internalv1pb.ListStringValue{}
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

func isGRPCTraceCorrelationHeaderKey(key string) bool {
	k := strings.ToLower(key)
	return k == tracebinMetadata
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
		md.Append(keyName, listVal.Values...)
	}
	return md
}

// IsGRPCProtocol checks if metadata is originated from gRPC API
func IsGRPCProtocol(internalMD DaprInternalMetadata) bool {
	var originContentType = ""
	if val, ok := internalMD[ContentTypeHeader]; ok {
		originContentType = val.Values[0]
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
		if isGRPCTraceCorrelationHeaderKey(k) {
			// add the grpc-trace-bin value to the http header as it contains the traceid
			traceContext := listVal.Values[0]
			if traceContext != "" {
				traceContextBinary := []byte(traceContext)
				sc, _ := propagation.FromBinary(traceContextBinary)
				setHeader(traceparentHeader, diag.SpanContextToString(sc))
			}
			continue
		}

		// Skip if the header key has -bin suffix except grpc-trace-bin
		if len(listVal.Values) == 0 || strings.HasSuffix(k, gRPCBinaryMetadataSuffix) || k == ContentTypeHeader {
			continue
		}
		setHeader(reservedGRPCMetadataToDaprPrefixHeader(k), listVal.Values[0])
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

// ErrorFromHTTPResponseCode converts http response code to gRPC status error
func ErrorFromHTTPResponseCode(code int, detail string) error {
	grpcCode := CodeFromHTTPStatus(code)
	if grpcCode == codes.OK {
		return nil
	}
	httpStatusText := http.StatusText(code)
	respStatus := grpc_status.New(grpcCode, httpStatusText)

	// Truncate detail string longer than 64 characters
	if len(detail) >= maxMetadataValueLen {
		detail = detail[:maxMetadataValueLen]
	}

	resps, err := respStatus.WithDetails(
		&epb.ErrorInfo{
			Type:   httpStatusText,
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

// ErrorFromInternalStatus converts internal status to gRPC status error
func ErrorFromInternalStatus(internalStatus *internalv1pb.Status) error {
	respStatus := &spb.Status{
		Code:    internalStatus.GetCode(),
		Message: internalStatus.GetMessage(),
		Details: internalStatus.GetDetails(),
	}

	return grpc_status.ErrorProto(respStatus)
}
