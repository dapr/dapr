// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package v1

import (
	"net/url"
	"strings"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/daprinternal/v1"
	"github.com/golang/protobuf/proto"
	structpb "github.com/golang/protobuf/ptypes/struct"
)

const (
	// DefaultAPIVersion is the default Dapr API version
	DefaultAPIVersion = commonv1pb.APIVersion_V1
)

// InvokeMethodRequest holds InternalInvokeRequest protobuf message
// and provides the helpers to manage it.
type InvokeMethodRequest struct {
	m *internalv1pb.InternalInvokeRequest
}

// NewInvokeMethodRequest creates InvokeMethodRequest object for method
func NewInvokeMethodRequest(method string) *InvokeMethodRequest {
	return &InvokeMethodRequest{
		m: &internalv1pb.InternalInvokeRequest{
			Ver:     DefaultAPIVersion,
			Message: &commonv1pb.InvokeRequest{Method: method},
		},
	}
}

// FromInvokeRequestMessage creates InvokeMethodRequest object from InvokeRequest pb object
func FromInvokeRequestMessage(pb *commonv1pb.InvokeRequest) *InvokeMethodRequest {
	return &InvokeMethodRequest{
		m: &internalv1pb.InternalInvokeRequest{
			Ver:     DefaultAPIVersion,
			Message: pb,
		},
	}
}

// FromInvokeMethodRequestProto creates InvokeMethodRequest object from InternalInvokeRequest pb object
func FromInvokeMethodRequestProto(pb *internalv1pb.InternalInvokeRequest) *InvokeMethodRequest {
	return &InvokeMethodRequest{m: pb}
}

// WithInvokeRequestProto sets Message to InvokeRequest pb object
func (imr *InvokeMethodRequest) WithInvokeRequestProto(pb *commonv1pb.InvokeRequest) *InvokeMethodRequest {
	imr.m.Message = proto.Clone(pb).(*commonv1pb.InvokeRequest)
	return imr
}

// WithMetadata sets metadata
func (imr *InvokeMethodRequest) WithMetadata(md map[string][]string) *InvokeMethodRequest {
	imr.m.Metadata = GrpcMetadataToInternalMetadata(md)
	return imr
}

// WithRawData sets message data and content_type
func (imr *InvokeMethodRequest) WithRawData(data []byte, contentType string) *InvokeMethodRequest {
	if contentType == "" {
		imr.m.Message.ContentType = JSONContentType
	}
	imr.m.Message.Data.Value = data
	return imr
}

// WithHTTPExtension sets new HTTP extension with verb and querystring
func (imr *InvokeMethodRequest) WithHTTPExtension(verb string, querystring string) *InvokeMethodRequest {
	httpMethod, ok := commonv1pb.HTTPExtension_Verb_value[strings.ToUpper(verb)]
	if !ok {
		httpMethod = int32(commonv1pb.HTTPExtension_POST)
	}

	var metadata map[string]string
	if querystring != "" {
		params, _ := url.ParseQuery(querystring)

		for k, v := range params {
			metadata[k] = v[0]
		}
	}

	imr.m.Message.ProtocolExtension = &commonv1pb.InvokeRequest_Http{
		Http: &commonv1pb.HTTPExtension{
			Verb:        commonv1pb.HTTPExtension_Verb(httpMethod),
			Querystring: metadata,
		},
	}

	return imr
}

// EncodeHTTPQueryString generates querystring for http using http extension object
func (imr *InvokeMethodRequest) EncodeHTTPQueryString() string {
	if imr.m.Message.GetHttp() == nil {
		return ""
	}

	qs := imr.m.Message.GetHttp().Querystring
	if len(qs) == 0 {
		return ""
	}

	params := url.Values{}
	for k, v := range qs {
		params.Add(k, v)
	}
	return params.Encode()
}

// APIVersion gets API version of InvokeMethodRequest
func (imr *InvokeMethodRequest) APIVersion() commonv1pb.APIVersion {
	return imr.m.Ver
}

// Metadata gets Metadata of InvokeMethodRequest
func (imr *InvokeMethodRequest) Metadata() *(map[string]*structpb.ListValue) {
	return &(imr.m.Metadata)
}

// Proto returns InternalInvokeRequest Proto object
func (imr *InvokeMethodRequest) Proto() *internalv1pb.InternalInvokeRequest {
	return proto.Clone(imr.m).(*internalv1pb.InternalInvokeRequest)
}

// Message gets InvokeRequest Message object
func (imr *InvokeMethodRequest) Message() *commonv1pb.InvokeRequest {
	return imr.m.Message
}
