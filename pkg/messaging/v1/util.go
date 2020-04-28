// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package v1

import (
	"net/http"
	"strings"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	"github.com/golang/protobuf/ptypes"
	"github.com/golang/protobuf/ptypes/any"
	structpb "github.com/golang/protobuf/ptypes/struct"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
)

const (
	// JSONContentType is the MIME media type for JSON
	JSONContentType = "application/json"
	// ProtobufContentType is the MIME media type for Protobuf
	ProtobufContentType = "application/x-protobuf"

	// DaprHeaderPrefix is the prefix if metadata is defined by non user-defined http headers
	DaprHeaderPrefix = "dapr-"

	// gRPCBinaryMetadata is the suffix of grpc metadata binary value
	gRPCBinaryMetadataSuffix = "-bin"
)

// GrpcMetadataToInternalMetadata converts gRPC metadata to dapr internal metadata map
func GrpcMetadataToInternalMetadata(md metadata.MD) map[string]*structpb.ListValue {
	var internalMD = map[string]*structpb.ListValue{}
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
		"User-Agent",
		"Via",
		"Warning":
		return true
	}
	return false
}

// InternalMetadataToGrpcMetadata converts internal metadata map to gRPC metadata
func InternalMetadataToGrpcMetadata(internalMD map[string]*structpb.ListValue, httpHeaderConversion bool) metadata.MD {
	var md = metadata.MD{}
	for k, listVal := range internalMD {
		keyName := strings.ToLower(k)
		if httpHeaderConversion && isPermanentHTTPHeader(k) {
			keyName = strings.ToLower(DaprHeaderPrefix + keyName)
		}
		for _, v := range listVal.Values {
			if _, ok := md[keyName]; !ok {
				md[keyName] = []string{v.GetStringValue()}
			} else {
				md[keyName] = append(md[keyName], v.GetStringValue())
			}
		}
	}
	return md
}

// InternalMetadataToHTTPHeader converts internal metadata pb to HTTP headers
func InternalMetadataToHTTPHeader(internalMD map[string]*structpb.ListValue, setHeader func(string, string)) {
	for k, listVal := range internalMD {
		// Skip if the header key has -bin suffix
		if len(listVal.Values) == 0 || strings.HasSuffix(k, gRPCBinaryMetadataSuffix) {
			continue
		}
		setHeader(k, listVal.Values[0].GetStringValue())
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

func isDataWithContentType(data *any.Any) bool {
	if data == nil {
		return false
	}

	return ptypes.Is(data, &commonv1pb.DataWithContentType{})
}

func extractRawData(data *any.Any) (string, []byte) {
	if !isDataWithContentType(data) {
		return "", nil
	}

	d := &commonv1pb.DataWithContentType{}
	if err := ptypes.UnmarshalAny(data, d); err != nil {
		return "", nil
	}

	return d.GetContentType(), d.GetBody()
}
