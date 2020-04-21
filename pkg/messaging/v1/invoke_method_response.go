// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package v1

import (
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/daprinternal/v1"
	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/ptypes"
	any "github.com/golang/protobuf/ptypes/any"
	structpb "github.com/golang/protobuf/ptypes/struct"
	"github.com/valyala/fasthttp"
	"google.golang.org/grpc/metadata"
)

// InvokeMethodResponse holds InternalInvokeResponse protobuf message
// and provides the helpers to manage it.
type InvokeMethodResponse struct {
	r *internalv1pb.InternalInvokeResponse
}

// NewInvokeMethodResponse returns new InvokeMethodResponse object with status
func NewInvokeMethodResponse(statusCode int32, statusMessage string, statusDetails []*any.Any) *InvokeMethodResponse {
	return &InvokeMethodResponse{
		r: &internalv1pb.InternalInvokeResponse{
			Status: &commonv1pb.Status{Code: statusCode, Message: statusMessage, Details: statusDetails},
		},
	}
}

// InternalInvokeResponse returns InvokeMethodResponse for InternalInvokeResponse pb to use the helpers
func InternalInvokeResponse(resp *internalv1pb.InternalInvokeResponse) *InvokeMethodResponse {
	return &InvokeMethodResponse{r: resp}
}

// WithMessage sets InvokeResponse pb object to Message field
func (imr *InvokeMethodResponse) WithMessage(pb *commonv1pb.InvokeResponse) *InvokeMethodResponse {
	imr.r.Message = pb
	return imr
}

// WithRawData sets Message using byte data and content type
func (imr *InvokeMethodResponse) WithRawData(data []byte, contentType string) *InvokeMethodResponse {
	d := &commonv1pb.DataWithContentType{ContentType: contentType, Body: data}
	imr.r.Message = &commonv1pb.InvokeResponse{}
	imr.r.Message.Data, _ = ptypes.MarshalAny(d)

	return imr
}

// WithHeaders sets gRPC repsonse header metadata
func (imr *InvokeMethodResponse) WithHeaders(headers metadata.MD) *InvokeMethodResponse {
	imr.r.Headers = GrpcMetadataToInternalMetadata(headers)
	return imr
}

// WithFastHTTPHeaders populates fasthttp response header to gRPC header metadata
func (imr *InvokeMethodResponse) WithFastHTTPHeaders(header *fasthttp.ResponseHeader) *InvokeMethodResponse {
	var md = map[string]*structpb.ListValue{}
	header.VisitAll(func(key []byte, value []byte) {
		md[string(key)] = &structpb.ListValue{
			Values: []*structpb.Value{
				{Kind: &structpb.Value_StringValue{StringValue: string(value)}},
			},
		}
	})
	if len(md) > 0 {
		imr.r.Headers = md
	}
	return imr
}

// WithTrailers sets Trailer in internal InvokeMethodResponse
func (imr *InvokeMethodResponse) WithTrailers(trailer metadata.MD) *InvokeMethodResponse {
	imr.r.Trailers = GrpcMetadataToInternalMetadata(trailer)
	return imr
}

// Status gets Response status
func (imr *InvokeMethodResponse) Status() *commonv1pb.Status {
	return imr.r.GetStatus()
}

// IsHTTPResponse returns true if response status code is http response status
func (imr *InvokeMethodResponse) IsHTTPResponse() bool {
	// gRPC status code <= 15 - https://github.com/grpc/grpc/blob/master/doc/statuscodes.md
	// HTTP status code >= 100 - https://tools.ietf.org/html/rfc2616#section-10
	return imr.r.GetStatus().Code >= 100
}

// Proto clones the internal InvokeMethodResponse pb object
func (imr *InvokeMethodResponse) Proto() *internalv1pb.InternalInvokeResponse {
	return proto.Clone(imr.r).(*internalv1pb.InternalInvokeResponse)
}

// Headers gets Headers metadata
func (imr *InvokeMethodResponse) Headers() *map[string]*structpb.ListValue {
	return &(imr.r.Headers)
}

// Trailers gets Trailers metadata
func (imr *InvokeMethodResponse) Trailers() *map[string]*structpb.ListValue {
	return &(imr.r.Trailers)
}

// Message returns message field in InvokeMethodResponse
func (imr *InvokeMethodResponse) Message() *commonv1pb.InvokeResponse {
	return imr.r.GetMessage()
}

// RawData returns content_type and byte array body
func (imr *InvokeMethodResponse) RawData() (string, []byte) {
	if imr.r.GetMessage() == nil {
		return "", nil
	}

	return extractRawData(imr.r.GetMessage().GetData())
}
