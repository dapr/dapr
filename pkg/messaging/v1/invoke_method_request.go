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
	"github.com/golang/protobuf/ptypes"
	structpb "github.com/golang/protobuf/ptypes/struct"
)

const (
	// DefaultAPIVersion is the default Dapr API version
	DefaultAPIVersion = internalv1pb.APIVersion_V1
)

// InvokeMethodRequest holds InternalInvokeRequest protobuf message
// and provides the helpers to manage it.
type InvokeMethodRequest struct {
	r *internalv1pb.InternalInvokeRequest
	m *commonv1pb.InvokeRequest
}

// NewInvokeMethodRequest creates InvokeMethodRequest object for method
func NewInvokeMethodRequest(method string) *InvokeMethodRequest {
	return &InvokeMethodRequest{
		r: &internalv1pb.InternalInvokeRequest{
			Ver: DefaultAPIVersion,
		},
		m: &commonv1pb.InvokeRequest{
			Method: method,
		},
	}
}

// FromInvokeRequestMessage creates InvokeMethodRequest object from InvokeRequest pb object
func FromInvokeRequestMessage(pb *commonv1pb.InvokeRequest) *InvokeMethodRequest {
	return &InvokeMethodRequest{
		r: &internalv1pb.InternalInvokeRequest{
			Ver: DefaultAPIVersion,
		},
		m: pb,
	}
}

// InternalInvokeRequest creates InvokeMethodRequest object from InternalInvokeRequest pb object
func InternalInvokeRequest(pb *internalv1pb.InternalInvokeRequest) (*InvokeMethodRequest, error) {
	req := &InvokeMethodRequest{r: pb}
	req.m = &commonv1pb.InvokeRequest{}
	if pb.Message != nil {
		if err := ptypes.UnmarshalAny(pb.Message, req.m); err != nil {
			return nil, err
		}
		pb.Message = nil
	}

	return req, nil
}

// WithMetadata sets metadata
func (imr *InvokeMethodRequest) WithMetadata(md map[string][]string) *InvokeMethodRequest {
	imr.r.Metadata = GrpcMetadataToInternalMetadata(md)
	return imr
}

// WithRawData sets message data and content_type
func (imr *InvokeMethodRequest) WithRawData(data []byte, contentType string) *InvokeMethodRequest {
	d := &commonv1pb.DataWithContentType{ContentType: contentType, Body: data}
	if contentType == "" {
		d.ContentType = JSONContentType
	}
	imr.m.Data, _ = ptypes.MarshalAny(d)
	return imr
}

// WithHTTPExtension sets new HTTP extension with verb and querystring
func (imr *InvokeMethodRequest) WithHTTPExtension(verb string, querystring string) *InvokeMethodRequest {
	httpMethod, ok := commonv1pb.HTTPExtension_Verb_value[strings.ToUpper(verb)]
	if !ok {
		httpMethod = int32(commonv1pb.HTTPExtension_POST)
	}

	var metadata = map[string]string{}
	if querystring != "" {
		params, _ := url.ParseQuery(querystring)

		for k, v := range params {
			metadata[k] = v[0]
		}
	}

	imr.m.HttpExtension = &commonv1pb.HTTPExtension{
		Verb:        commonv1pb.HTTPExtension_Verb(httpMethod),
		Querystring: metadata,
	}

	return imr
}

// EncodeHTTPQueryString generates querystring for http using http extension object
func (imr *InvokeMethodRequest) EncodeHTTPQueryString() string {
	if imr.m.GetHttpExtension() == nil {
		return ""
	}

	qs := imr.m.GetHttpExtension().Querystring
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
func (imr *InvokeMethodRequest) APIVersion() internalv1pb.APIVersion {
	return imr.r.GetVer()
}

// Metadata gets Metadata of InvokeMethodRequest
func (imr *InvokeMethodRequest) Metadata() map[string]*structpb.ListValue {
	return imr.r.GetMetadata()
}

// Proto returns InternalInvokeRequest Proto object
func (imr *InvokeMethodRequest) Proto() *internalv1pb.InternalInvokeRequest {
	p := proto.Clone(imr.r).(*internalv1pb.InternalInvokeRequest)
	if imr.m != nil {
		p.Message, _ = ptypes.MarshalAny(imr.m)
	}
	return p
}

// Message gets InvokeRequest Message object
func (imr *InvokeMethodRequest) Message() *commonv1pb.InvokeRequest {
	return imr.m
}

// RawData returns content_type and byte array body
func (imr *InvokeMethodRequest) RawData() (string, []byte) {
	if imr.m == nil {
		return "", nil
	}
	return extractRawData(imr.m.GetData())
}
