// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package v1

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
	"google.golang.org/protobuf/types/known/anypb"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

func TestInvokeRequest(t *testing.T) {
	req := NewInvokeMethodRequest("test_method")

	assert.Equal(t, internalv1pb.APIVersion_V1, req.r.GetVer())
	assert.Equal(t, "test_method", req.r.Message.GetMethod())
}

func TestFromInvokeRequestMessage(t *testing.T) {
	pb := &commonv1pb.InvokeRequest{Method: "frominvokerequestmessage"}
	req := FromInvokeRequestMessage(pb)

	assert.Equal(t, internalv1pb.APIVersion_V1, req.r.GetVer())
	assert.Equal(t, "frominvokerequestmessage", req.r.Message.GetMethod())
}

func TestInternalInvokeRequest(t *testing.T) {
	t.Run("valid internal invoke request", func(t *testing.T) {
		m := &commonv1pb.InvokeRequest{
			Method:      "invoketest",
			ContentType: "application/json",
			Data:        &anypb.Any{Value: []byte("test")},
		}
		pb := internalv1pb.InternalInvokeRequest{
			Ver:     internalv1pb.APIVersion_V1,
			Message: m,
		}

		ir, err := InternalInvokeRequest(&pb)
		assert.NoError(t, err)
		assert.NotNil(t, ir.r.Message)
		assert.Equal(t, "invoketest", ir.r.Message.GetMethod())
		assert.NotNil(t, ir.r.Message.GetData())
	})

	t.Run("nil message field", func(t *testing.T) {
		pb := internalv1pb.InternalInvokeRequest{
			Ver:     internalv1pb.APIVersion_V1,
			Message: nil,
		}

		_, err := InternalInvokeRequest(&pb)
		assert.Error(t, err)
	})
}

func TestMetadata(t *testing.T) {
	t.Run("gRPC headers", func(t *testing.T) {
		req := NewInvokeMethodRequest("test_method")
		md := map[string][]string{
			"test1": {"val1", "val2"},
			"test2": {"val3", "val4"},
		}
		req.WithMetadata(md)
		mdata := req.Metadata()

		assert.Equal(t, "val1", mdata["test1"].GetValues()[0])
		assert.Equal(t, "val2", mdata["test1"].GetValues()[1])
		assert.Equal(t, "val3", mdata["test2"].GetValues()[0])
		assert.Equal(t, "val4", mdata["test2"].GetValues()[1])
	})

	t.Run("HTTP headers", func(t *testing.T) {
		req := fasthttp.AcquireRequest()
		req.Header.Set("Header1", "Value1")
		req.Header.Set("Header2", "Value2")
		req.Header.Set("Header3", "Value3")

		re := NewInvokeMethodRequest("test_method")
		re.WithFastHTTPHeaders(&req.Header)
		mheader := re.Metadata()

		assert.Equal(t, "Value1", mheader["Header1"].GetValues()[0])
		assert.Equal(t, "Value2", mheader["Header2"].GetValues()[0])
		assert.Equal(t, "Value3", mheader["Header3"].GetValues()[0])
	})
}

func TestData(t *testing.T) {
	t.Run("contenttype is set", func(t *testing.T) {
		resp := NewInvokeMethodRequest("test_method")
		resp.WithRawData([]byte("test"), "application/json")
		contentType, bData := resp.RawData()
		assert.Equal(t, "application/json", contentType)
		assert.Equal(t, []byte("test"), bData)
	})

	t.Run("contenttype is unset", func(t *testing.T) {
		resp := NewInvokeMethodRequest("test_method")
		resp.WithRawData([]byte("test"), "")
		contentType, bData := resp.RawData()
		assert.Equal(t, "application/json", contentType)
		assert.Equal(t, []byte("test"), bData)
	})

	t.Run("typeurl is set but content_type is unset", func(t *testing.T) {
		resp := NewInvokeMethodRequest("test_method")
		resp.r.Message.Data = &anypb.Any{TypeUrl: "fake", Value: []byte("fake")}
		contentType, bData := resp.RawData()
		assert.Equal(t, "", contentType)
		assert.Equal(t, []byte("fake"), bData)
	})
}

func TestHTTPExtension(t *testing.T) {
	req := NewInvokeMethodRequest("test_method")
	req.WithHTTPExtension("POST", "query1=value1&query2=value2")
	assert.Equal(t, commonv1pb.HTTPExtension_POST, req.Message().GetHttpExtension().GetVerb())
	assert.Equal(t, "query1=value1&query2=value2", req.EncodeHTTPQueryString())
}

func TestActor(t *testing.T) {
	req := NewInvokeMethodRequest("test_method")
	req.WithActor("testActor", "1")
	assert.Equal(t, "testActor", req.Actor().GetActorType())
	assert.Equal(t, "1", req.Actor().GetActorId())
}

func TestProto(t *testing.T) {
	m := &commonv1pb.InvokeRequest{
		Method:      "invoketest",
		ContentType: "application/json",
		Data:        &anypb.Any{Value: []byte("test")},
	}
	pb := internalv1pb.InternalInvokeRequest{
		Ver:     internalv1pb.APIVersion_V1,
		Message: m,
	}

	ir, err := InternalInvokeRequest(&pb)
	assert.NoError(t, err)
	req2 := ir.Proto()

	assert.Equal(t, "application/json", req2.GetMessage().ContentType)
	assert.Equal(t, []byte("test"), req2.GetMessage().Data.Value)
}

func TestAddHeaders(t *testing.T) {
	req := NewInvokeMethodRequest("test_method")
	header := fasthttp.RequestHeader{}
	header.Add("Dapr-Reentrant-Id", "test")
	req.AddHeaders(&header)

	assert.NotNil(t, req.r.Metadata)
	assert.NotNil(t, req.r.Metadata["Dapr-Reentrant-Id"])
	assert.Equal(t, "test", req.r.Metadata["Dapr-Reentrant-Id"].Values[0])
}

func TestAddHeadersDoesNotOverwrite(t *testing.T) {
	header := fasthttp.RequestHeader{}
	header.Add("Dapr-Reentrant-Id", "test")
	req := NewInvokeMethodRequest("test_method").WithFastHTTPHeaders(&header)

	header.Set("Dapr-Reentrant-Id", "test2")
	req.AddHeaders(&header)

	assert.NotNil(t, req.r.Metadata)
	assert.NotNil(t, req.r.Metadata["Dapr-Reentrant-Id"])
	assert.Equal(t, "test", req.r.Metadata["Dapr-Reentrant-Id"].Values[0])
}

func TestWithCustomHTTPMetadata(t *testing.T) {
	customMetadataKey := func(i int) string {
		return fmt.Sprintf("customMetadataKey%d", i)
	}
	customMetadataValue := func(i int) string {
		return fmt.Sprintf("customMetadataValue%d", i)
	}

	numMetadata := 10
	md := make(map[string]string, numMetadata)
	for i := 0; i < numMetadata; i++ {
		md[customMetadataKey(i)] = customMetadataValue(i)
	}

	req := NewInvokeMethodRequest("test_method")
	req.WithCustomHTTPMetadata(md)

	imrMd := req.Metadata()
	for i := 0; i < numMetadata; i++ {
		val, ok := imrMd[customMetadataKey(i)]
		assert.True(t, ok)
		// We assume only 1 value per key as the input map can only support string -> string mapping.
		assert.Equal(t, customMetadataValue(i), val.Values[0])
	}
}
