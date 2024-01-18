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

package internals

import (
	"encoding/base64"
	"net/http"
	"strings"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	"google.golang.org/protobuf/types/known/anypb"
)

const (
	// GRPCContentType is the MIME media type for grpc.
	GRPCContentType = "application/grpc"
	// JSONContentType is the MIME media type for JSON.
	JSONContentType = "application/json"
	// ProtobufContentType is the MIME media type for Protobuf.
	ProtobufContentType = "application/x-protobuf"
	// OctetStreamContentType is the MIME media type for arbitrary binary data.
	OctetStreamContentType = "application/octet-stream"
)

const (
	// Separator for strings containing multiple tokens.
	daprSeparator = "||"
	// gRPCBinaryMetadata is the suffix of grpc metadata binary value.
	gRPCBinaryMetadataSuffix = "-bin"
)

// This file contains additional, hand-written methods added to the generated objects.

// GetActorKey returns the key for the actor.
func (x *Actor) GetActorKey() string {
	if x == nil {
		return ""
	}
	return x.GetActorType() + daprSeparator + x.GetActorId()
}

// NewInternalInvokeRequest returns an InternalInvokeRequest with the given method
func NewInternalInvokeRequest(method string) *InternalInvokeRequest {
	return &InternalInvokeRequest{
		Ver: APIVersion_V1,
		Message: &commonv1pb.InvokeRequest{
			Method: method,
		},
	}
}

// WithActor sets actor type and id.
func (x *InternalInvokeRequest) WithActor(actorType, actorID string) *InternalInvokeRequest {
	x.Actor = &Actor{
		ActorType: actorType,
		ActorId:   actorID,
	}
	return x
}

// WithData sets the data.
func (x *InternalInvokeRequest) WithData(data []byte) *InternalInvokeRequest {
	if x.Message.Data == nil {
		x.Message.Data = &anypb.Any{}
	}
	x.Message.Data.Value = data
	return x
}

// WithContentType sets the content type.
func (x *InternalInvokeRequest) WithContentType(contentType string) *InternalInvokeRequest {
	x.Message.ContentType = contentType
	return x
}

// WithDataTypeURL sets the type_url property for the data.
// When a type_url is set, the Content-Type automatically becomes the protobuf one.
func (x *InternalInvokeRequest) WithDataTypeURL(val string) *InternalInvokeRequest {
	if x.Message.Data == nil {
		x.Message.Data = &anypb.Any{}
	}
	x.Message.Data.TypeUrl = val
	x.Message.ContentType = ProtobufContentType
	return x
}

// WithHTTPExtension sets new HTTP extension with verb and querystring.
func (x *InternalInvokeRequest) WithHTTPExtension(verb string, querystring string) *InternalInvokeRequest {
	httpMethod, ok := commonv1pb.HTTPExtension_Verb_value[strings.ToUpper(verb)]
	if !ok {
		httpMethod = int32(commonv1pb.HTTPExtension_POST)
	}

	x.Message.HttpExtension = &commonv1pb.HTTPExtension{
		Verb:        commonv1pb.HTTPExtension_Verb(httpMethod),
		Querystring: querystring,
	}

	return x
}

// WithMetadata sets metadata.
func (x *InternalInvokeRequest) WithMetadata(md map[string][]string) *InternalInvokeRequest {
	x.Metadata = MetadataToInternalMetadata(md)
	return x
}

// WithHTTPHeaders sets metadata from HTTP request headers.
func (x *InternalInvokeRequest) WithHTTPHeaders(header http.Header) *InternalInvokeRequest {
	x.Metadata = HTTPHeadersToInternalMetadata(header)
	return x
}

// WithFastHTTPHeaders sets metadata from fasthttp request headers.
func (x *InternalInvokeRequest) WithFastHTTPHeaders(header fasthttpHeaders) *InternalInvokeRequest {
	x.Metadata = FastHTTPHeadersToInternalMetadata(header)
	return x
}

// IsHTTPResponse returns true if response status code is http response status.
func (x *InternalInvokeResponse) IsHTTPResponse() bool {
	if x == nil {
		return false
	}
	// gRPC status code <= 15 - https://github.com/grpc/grpc/blob/master/doc/statuscodes.md
	// HTTP status code >= 100 - https://tools.ietf.org/html/rfc2616#section-10
	return x.GetStatus().Code >= 100
}

// MetadataToInternalMetadata converts metadata to Dapr internal metadata map.
func MetadataToInternalMetadata(md map[string][]string) map[string]*ListStringValue {
	internalMD := make(map[string]*ListStringValue, len(md))
	for k, values := range md {
		if strings.HasSuffix(k, gRPCBinaryMetadataSuffix) {
			// Binary key requires base64 encoding for the value
			vals := make([]string, len(values))
			for i, val := range values {
				vals[i] = base64.StdEncoding.EncodeToString([]byte(val))
			}
			internalMD[k] = &ListStringValue{
				Values: vals,
			}
		} else {
			internalMD[k] = &ListStringValue{
				Values: values,
			}
		}
	}

	return internalMD
}

// HTTPHeadersToInternalMetadata converts http headers to Dapr internal metadata map.
func HTTPHeadersToInternalMetadata(header http.Header) map[string]*ListStringValue {
	internalMD := make(map[string]*ListStringValue, len(header))
	for key, val := range header {
		// Note: HTTP headers can never be binary (only gRPC supports binary headers)
		if internalMD[key] == nil || len(internalMD[key].Values) == 0 {
			internalMD[key] = &ListStringValue{
				Values: val,
			}
		} else {
			internalMD[key].Values = append(internalMD[key].Values, val...)
		}
	}
	return internalMD
}

// Covers *fasthttp.RequestHeader and *fasthttp.ResponseHeader
type fasthttpHeaders interface {
	Len() int
	VisitAll(f func(key []byte, value []byte))
}

// FastHTTPHeadersToInternalMetadata converts fasthttp headers to Dapr internal metadata map.
func FastHTTPHeadersToInternalMetadata(header fasthttpHeaders) map[string]*ListStringValue {
	internalMD := make(map[string]*ListStringValue, header.Len())
	header.VisitAll(func(key []byte, value []byte) {
		// Note: fasthttp headers can never be binary (only gRPC supports binary headers)
		keyStr := string(key)
		if internalMD[keyStr] == nil || len(internalMD[keyStr].Values) == 0 {
			internalMD[keyStr] = &ListStringValue{
				Values: []string{string(value)},
			}
		} else {
			internalMD[keyStr].Values = append(internalMD[keyStr].Values, string(value))
		}
	})
	return internalMD
}
