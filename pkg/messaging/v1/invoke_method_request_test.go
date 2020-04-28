// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package v1

import (
	"testing"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/daprinternal/v1"
	"github.com/golang/protobuf/ptypes"
	"github.com/stretchr/testify/assert"
)

func TestInvokeRequest(t *testing.T) {
	req := NewInvokeMethodRequest("test_method")

	assert.Equal(t, internalv1pb.APIVersion_V1, req.r.GetVer())
	assert.Equal(t, "test_method", req.m.GetMethod())
}

func TestFromInvokeRequestMessage(t *testing.T) {
	pb := &commonv1pb.InvokeRequest{Method: "frominvokerequestmessage"}
	req := FromInvokeRequestMessage(pb)

	assert.Equal(t, internalv1pb.APIVersion_V1, req.r.GetVer())
	assert.Equal(t, "frominvokerequestmessage", req.m.GetMethod())
}

func TestInternalInvokeRequest(t *testing.T) {
	d := commonv1pb.DataWithContentType{
		ContentType: "application/json",
		Body:        []byte("test"),
	}
	ds, _ := ptypes.MarshalAny(&d)
	m := commonv1pb.InvokeRequest{
		Method: "invoketest",
		Data:   ds,
	}
	ms, _ := ptypes.MarshalAny(&m)
	pb := internalv1pb.InternalInvokeRequest{
		Ver:     internalv1pb.APIVersion_V1,
		Message: ms,
	}

	ir, err := InternalInvokeRequest(&pb)
	assert.NoError(t, err)
	assert.NotNil(t, ir.m)
	assert.Equal(t, "invoketest", ir.m.GetMethod())
	assert.NotNil(t, ir.m.GetData())
}

func TestMetadata(t *testing.T) {
	req := NewInvokeMethodRequest("test_method")
	md := map[string][]string{
		"test1": {"val1", "val2"},
		"test2": {"val3", "val4"},
	}
	req.WithMetadata(md)
	mdata := req.Metadata()

	assert.Equal(t, "val1", mdata["test1"].GetValues()[0].GetStringValue())
	assert.Equal(t, "val2", mdata["test1"].GetValues()[1].GetStringValue())
	assert.Equal(t, "val3", mdata["test2"].GetValues()[0].GetStringValue())
	assert.Equal(t, "val4", mdata["test2"].GetValues()[1].GetStringValue())
}

func TestData(t *testing.T) {
	req := NewInvokeMethodRequest("test_method")
	req.WithRawData([]byte("test"), "application/json")
	contentType, bData := req.RawData()
	assert.Equal(t, "application/json", contentType)
	assert.Equal(t, []byte("test"), bData)
}

func TestHTTPExtension(t *testing.T) {
	req := NewInvokeMethodRequest("test_method")
	req.WithHTTPExtension("POST", "query1=value1&query2=value2")
	assert.Equal(t, commonv1pb.HTTPExtension_POST, req.Message().GetHttpExtension().GetVerb())
	assert.Equal(t, "query1=value1&query2=value2", req.EncodeHTTPQueryString())
}

func TestProto(t *testing.T) {
	d := commonv1pb.DataWithContentType{
		ContentType: "application/json",
		Body:        []byte("test"),
	}
	ds, _ := ptypes.MarshalAny(&d)
	m := commonv1pb.InvokeRequest{
		Method: "invoketest",
		Data:   ds,
	}
	ms, _ := ptypes.MarshalAny(&m)
	pb := internalv1pb.InternalInvokeRequest{
		Ver:     internalv1pb.APIVersion_V1,
		Message: ms,
	}

	ir, err := InternalInvokeRequest(&pb)
	assert.NoError(t, err)
	req2 := ir.Proto()

	m2 := commonv1pb.InvokeRequest{}
	err = ptypes.UnmarshalAny(req2.GetMessage(), &m2)
	assert.NoError(t, err)

	d2 := commonv1pb.DataWithContentType{}
	err = ptypes.UnmarshalAny(m2.Data, &d2)
	assert.NoError(t, err)

	assert.Equal(t, "application/json", d2.GetContentType())
	assert.Equal(t, []byte("test"), d2.GetBody())
}
