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
	JSONContentType     = "application/json"
	ProtobufContentType = "application/x-protobuf"

	DefaultAPIVersion = commonv1pb.APIVersion_V1
)

type InvokeMethodRequest struct {
	m *internalv1pb.InternalInvokeRequest
}

func NewInvokeMethodRequest(method string) *InvokeMethodRequest {
	return &InvokeMethodRequest{
		m: &internalv1pb.InternalInvokeRequest{
			Ver:     commonv1pb.APIVersion_V1,
			Message: &commonv1pb.InvokeRequest{Method: method},
		},
	}
}

func FromInvokeRequestMessage(pb *commonv1pb.InvokeRequest) *InvokeMethodRequest {
	return &InvokeMethodRequest{
		m: &internalv1pb.InternalInvokeRequest{
			Ver:     commonv1pb.APIVersion_V1,
			Message: pb,
		},
	}
}

func FromInvokeMethodRequestProto(pb *internalv1pb.InternalInvokeRequest) *InvokeMethodRequest {
	return &InvokeMethodRequest{m: pb}
}

func (imr *InvokeMethodRequest) WithInvokeRequestProto(pb *commonv1pb.InvokeRequest) *InvokeMethodRequest {
	imr.m.Message = pb
	return imr
}

func (imr *InvokeMethodRequest) WithMetadata(md map[string][]string) *InvokeMethodRequest {
	imr.m.Metadata = GrpcMetadataToInternalMetadata(md)
	return imr
}

func (imr *InvokeMethodRequest) WithRawData(data []byte, contentType string) *InvokeMethodRequest {
	if contentType == "" {
		imr.m.Message.ContentType = JSONContentType
	}
	imr.m.Message.Data.Value = data
	return imr
}

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

func (imr *InvokeMethodRequest) APIVersion() commonv1pb.APIVersion {
	return imr.m.Ver
}

func (imr *InvokeMethodRequest) Metadata() *(map[string]*structpb.ListValue) {
	return &(imr.m.Metadata)
}

func (imr *InvokeMethodRequest) Proto() *internalv1pb.InternalInvokeRequest {
	return proto.Clone(imr.m).(*internalv1pb.InternalInvokeRequest)
}

func (imr *InvokeMethodRequest) Message() *commonv1pb.InvokeRequest {
	return imr.m.Message
}
