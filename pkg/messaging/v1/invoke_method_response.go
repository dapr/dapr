package v1

import (
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/daprinternal/v1"
	"github.com/golang/protobuf/proto"
	any "github.com/golang/protobuf/ptypes/any"
	structpb "github.com/golang/protobuf/ptypes/struct"
	"github.com/valyala/fasthttp"
	"google.golang.org/grpc/metadata"
)

type InvokeMethodResponse struct {
	r *internalv1pb.InternalInvokeResponse
}

func NewInvokeMethodResponse(statusCode int32, statusMessage string, statusDetails *[]*any.Any) *InvokeMethodResponse {
	return &InvokeMethodResponse{
		r: &internalv1pb.InternalInvokeResponse{
			Status: &commonv1pb.Status{Code: statusCode, Message: statusMessage, Details: *statusDetails},
		},
	}
}

func FromInternalInvokeResponse(resp *internalv1pb.InternalInvokeResponse) *InvokeMethodResponse {
	return &InvokeMethodResponse{r: resp}
}

func (imr *InvokeMethodResponse) WithInvokeResponseProto(pb *commonv1pb.InvokeResponse) *InvokeMethodResponse {
	imr.r.Message = pb
	return imr
}

func (imr *InvokeMethodResponse) WithRawData(data []byte, contentType string) *InvokeMethodResponse {
	imr.r.Message = &commonv1pb.InvokeResponse{}
	imr.r.Message.Data.Value = data
	imr.r.Message.ContentType = contentType

	return imr
}

func (imr *InvokeMethodResponse) WithHeaders(headers metadata.MD) *InvokeMethodResponse {
	imr.r.Headers = GrpcMetadataToInternalMetadata(headers)
	return imr
}

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

func (imr *InvokeMethodResponse) WithTrailers(headers metadata.MD) *InvokeMethodResponse {
	imr.r.Trailers = GrpcMetadataToInternalMetadata(headers)
	return imr
}

func (imr *InvokeMethodResponse) Status() *commonv1pb.Status {
	return imr.r.Status
}

func (imr *InvokeMethodResponse) IsHTTPResponse() bool {
	return imr.r.Status.Code >= 100
}

func (imr *InvokeMethodResponse) Proto() *internalv1pb.InternalInvokeResponse {
	return proto.Clone(imr.r).(*internalv1pb.InternalInvokeResponse)
}

func (imr *InvokeMethodResponse) Headers() *map[string]*structpb.ListValue {
	return &(imr.r.Headers)
}

func (imr *InvokeMethodResponse) Trailers() *map[string]*structpb.ListValue {
	return &(imr.r.Trailers)
}

func (imr *InvokeMethodResponse) Message() *commonv1pb.InvokeResponse {
	return imr.r.Message
}
