// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package v1

import (
	"net/http"
	"testing"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/daprinternal/v1"
	"github.com/golang/protobuf/ptypes"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
	"google.golang.org/grpc/codes"
)

func TestInvocationResponse(t *testing.T) {
	req := NewInvokeMethodResponse(0, "OK", nil)

	assert.Equal(t, int32(0), req.r.GetStatus().Code)
	assert.Equal(t, "OK", req.r.GetStatus().Message)
	assert.NotNil(t, req.m)
}

func TestInternalInvocationResponse(t *testing.T) {
	d := commonv1pb.DataWithContentType{
		ContentType: "application/json",
		Body:        []byte("response"),
	}
	ds, _ := ptypes.MarshalAny(&d)
	m := commonv1pb.InvokeResponse{
		Data: ds,
	}
	ms, _ := ptypes.MarshalAny(&m)
	pb := internalv1pb.InternalInvokeResponse{
		Status:  &internalv1pb.Status{Code: 0},
		Message: ms,
	}

	ir, err := InternalInvokeResponse(&pb)
	assert.NoError(t, err)
	assert.NotNil(t, ir.m)
	assert.Equal(t, int32(0), ir.r.GetStatus().GetCode())
}

func TestResponseData(t *testing.T) {
	resp := NewInvokeMethodResponse(0, "OK", nil)
	resp.WithRawData([]byte("test"), "application/json")
	contentType, bData := resp.RawData()
	assert.Equal(t, "application/json", contentType)
	assert.Equal(t, []byte("test"), bData)
}

func TestResponseHeader(t *testing.T) {
	t.Run("gRPC headers", func(t *testing.T) {
		resp := NewInvokeMethodResponse(0, "OK", nil)
		md := map[string][]string{
			"test1": {"val1", "val2"},
			"test2": {"val3", "val4"},
		}
		resp.WithHeaders(md)
		mheader := resp.Headers()

		assert.Equal(t, "val1", mheader["test1"].GetValues()[0].GetStringValue())
		assert.Equal(t, "val2", mheader["test1"].GetValues()[1].GetStringValue())
		assert.Equal(t, "val3", mheader["test2"].GetValues()[0].GetStringValue())
		assert.Equal(t, "val4", mheader["test2"].GetValues()[1].GetStringValue())
	})

	t.Run("HTTP headers", func(t *testing.T) {
		var resp = fasthttp.AcquireResponse()
		resp.Header.Set("Header1", "Value1")
		resp.Header.Set("Header2", "Value2")
		resp.Header.Set("Header3", "Value3")

		re := NewInvokeMethodResponse(0, "OK", nil)
		re.WithFastHTTPHeaders(&resp.Header)
		mheader := re.Headers()

		assert.Equal(t, "Value1", mheader["Header1"].GetValues()[0].GetStringValue())
		assert.Equal(t, "Value2", mheader["Header2"].GetValues()[0].GetStringValue())
		assert.Equal(t, "Value3", mheader["Header3"].GetValues()[0].GetStringValue())
	})
}

func TestResponseTrailer(t *testing.T) {
	resp := NewInvokeMethodResponse(0, "OK", nil)
	md := map[string][]string{
		"test1": {"val1", "val2"},
		"test2": {"val3", "val4"},
	}
	resp.WithTrailers(md)
	mheader := resp.Trailers()

	assert.Equal(t, "val1", mheader["test1"].GetValues()[0].GetStringValue())
	assert.Equal(t, "val2", mheader["test1"].GetValues()[1].GetStringValue())
	assert.Equal(t, "val3", mheader["test2"].GetValues()[0].GetStringValue())
	assert.Equal(t, "val4", mheader["test2"].GetValues()[1].GetStringValue())
}

func TestIsHTTPResponse(t *testing.T) {
	t.Run("gRPC response status", func(t *testing.T) {
		grpcResp := NewInvokeMethodResponse(int32(codes.OK), "OK", nil)
		assert.False(t, grpcResp.IsHTTPResponse())
	})

	t.Run("HTTP response status", func(t *testing.T) {
		httpResp := NewInvokeMethodResponse(http.StatusOK, "OK", nil)
		assert.True(t, httpResp.IsHTTPResponse())
	})
}
