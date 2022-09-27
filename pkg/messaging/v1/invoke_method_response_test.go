/*
Copyright 2021 The Dapr Authors
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

package v1

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/anypb"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

func TestInvocationResponse(t *testing.T) {
	resp := NewInvokeMethodResponse(0, "OK", nil)
	defer resp.Close()

	assert.Equal(t, int32(0), resp.r.GetStatus().Code)
	assert.Equal(t, "OK", resp.r.GetStatus().Message)
	assert.NotNil(t, resp.r.Message)
}

func TestInternalInvocationResponse(t *testing.T) {
	t.Run("valid internal invoke response with no data", func(t *testing.T) {
		m := &commonv1pb.InvokeResponse{
			Data:        nil,
			ContentType: "application/json",
		}
		pb := internalv1pb.InternalInvokeResponse{
			Status:  &internalv1pb.Status{Code: 0},
			Message: m,
		}

		ir, err := InternalInvokeResponse(&pb)
		assert.NoError(t, err)
		defer ir.Close()
		assert.NotNil(t, ir.r.Message)
		assert.Equal(t, int32(0), ir.r.GetStatus().GetCode())
		assert.Nil(t, ir.r.Message.GetData())

		bData, err := io.ReadAll(ir.RawData())
		assert.NoError(t, err)
		assert.Len(t, bData, 0)
	})

	t.Run("valid internal invoke response with data", func(t *testing.T) {
		m := &commonv1pb.InvokeResponse{
			Data:        &anypb.Any{Value: []byte("test")},
			ContentType: "application/json",
		}
		pb := internalv1pb.InternalInvokeResponse{
			Status:  &internalv1pb.Status{Code: 0},
			Message: m,
		}

		ir, err := InternalInvokeResponse(&pb)
		assert.NoError(t, err)
		defer ir.Close()
		assert.NotNil(t, ir.r.Message)
		assert.Equal(t, int32(0), ir.r.GetStatus().GetCode())
		assert.NotNil(t, ir.r.Message.GetData())
		assert.Len(t, ir.r.Message.GetData().Value, 0)

		bData, err := io.ReadAll(ir.RawData())
		assert.NoError(t, err)
		assert.Equal(t, "test", string(bData))
	})

	t.Run("Message is nil", func(t *testing.T) {
		pb := internalv1pb.InternalInvokeResponse{
			Status:  &internalv1pb.Status{Code: 0},
			Message: nil,
		}

		ir, err := InternalInvokeResponse(&pb)
		assert.NoError(t, err)
		defer ir.Close()
		assert.NotNil(t, ir.r.Message)
		assert.Nil(t, ir.r.Message.GetData())
	})
}

func TestResponseData(t *testing.T) {
	t.Run("contenttype is set", func(t *testing.T) {
		resp := NewInvokeMethodResponse(0, "OK", nil)
		defer resp.Close()
		resp.WithRawDataString("test", "application/json")
		bData, err := io.ReadAll(resp.RawData())
		assert.NoError(t, err)
		contentType := resp.r.Message.ContentType
		assert.Equal(t, "application/json", contentType)
		assert.Equal(t, "test", string(bData))
	})

	t.Run("contenttype is unset", func(t *testing.T) {
		resp := NewInvokeMethodResponse(0, "OK", nil)
		defer resp.Close()

		resp.WithRawDataString("test", "")
		contentType := resp.ContentType()
		bData, err := io.ReadAll(resp.RawData())
		assert.NoError(t, err)
		assert.Equal(t, "", resp.r.Message.ContentType)
		assert.Equal(t, "", contentType)
		assert.Equal(t, "test", string(bData))

		// Force the ContentType to be empty to test setting it in RawData
		resp.WithRawDataString("test", "")
		resp.r.Message.ContentType = ""
		contentType = resp.ContentType()
		bData, err = io.ReadAll(resp.RawData())
		assert.NoError(t, err)
		assert.Equal(t, "", resp.r.Message.ContentType)
		assert.Equal(t, "", contentType)
		assert.Equal(t, "test", string(bData))
	})

	t.Run("typeurl is set but content_type is unset", func(t *testing.T) {
		s := &commonv1pb.StateItem{Key: "custom_key"}
		b, err := anypb.New(s)
		assert.NoError(t, err)

		resp := NewInvokeMethodResponse(0, "OK", nil)
		defer resp.Close()
		resp.r.Message.Data = b
		contentType := resp.ContentType()
		bData, err := io.ReadAll(resp.RawData())
		assert.NoError(t, err)
		assert.Equal(t, ProtobufContentType, contentType)
		assert.Equal(t, b.Value, bData)
	})
}

func TestResponseProto(t *testing.T) {
	t.Run("byte slice", func(t *testing.T) {
		m := &commonv1pb.InvokeResponse{
			Data:        &anypb.Any{Value: []byte("test")},
			ContentType: "application/json",
		}
		pb := internalv1pb.InternalInvokeResponse{
			Status:  &internalv1pb.Status{Code: 0},
			Message: m,
		}

		ir, err := InternalInvokeResponse(&pb)
		assert.NoError(t, err)
		defer ir.Close()
		req2 := ir.Proto()

		assert.Equal(t, "application/json", req2.GetMessage().ContentType)
		assert.Len(t, req2.GetMessage().Data.Value, 0)

		bData, err := io.ReadAll(ir.RawData())
		assert.NoError(t, err)
		assert.Equal(t, []byte("test"), bData)
	})

	t.Run("stream", func(t *testing.T) {
		m := &commonv1pb.InvokeResponse{
			ContentType: "application/json",
		}
		pb := internalv1pb.InternalInvokeResponse{
			Status:  &internalv1pb.Status{Code: 0},
			Message: m,
		}

		ir, err := InternalInvokeResponse(&pb)
		assert.NoError(t, err)
		defer ir.Close()
		ir.data = io.NopCloser(strings.NewReader("test"))
		req2 := ir.Proto()

		assert.Equal(t, "application/json", req2.GetMessage().ContentType)
		assert.Nil(t, req2.GetMessage().Data)

		bData, err := io.ReadAll(ir.RawData())
		assert.NoError(t, err)
		assert.Equal(t, []byte("test"), bData)
	})
}

func TestResponseProtoWithData(t *testing.T) {
	t.Run("byte slice", func(t *testing.T) {
		m := &commonv1pb.InvokeResponse{
			Data:        &anypb.Any{Value: []byte("test")},
			ContentType: "application/json",
		}
		pb := internalv1pb.InternalInvokeResponse{
			Status:  &internalv1pb.Status{Code: 0},
			Message: m,
		}

		ir, err := InternalInvokeResponse(&pb)
		assert.NoError(t, err)
		defer ir.Close()
		req2, err := ir.ProtoWithData()
		assert.NoError(t, err)

		assert.Equal(t, "application/json", req2.GetMessage().ContentType)
		assert.Equal(t, []byte("test"), req2.GetMessage().Data.Value)
	})

	t.Run("stream", func(t *testing.T) {
		m := &commonv1pb.InvokeResponse{
			ContentType: "application/json",
		}
		pb := internalv1pb.InternalInvokeResponse{
			Status:  &internalv1pb.Status{Code: 0},
			Message: m,
		}

		ir, err := InternalInvokeResponse(&pb)
		assert.NoError(t, err)
		defer ir.Close()
		ir.data = io.NopCloser(strings.NewReader("test"))
		req2, err := ir.ProtoWithData()
		assert.NoError(t, err)

		assert.Equal(t, "application/json", req2.GetMessage().ContentType)
		assert.Equal(t, []byte("test"), req2.GetMessage().Data.Value)
	})
}

func TestResponseHeader(t *testing.T) {
	t.Run("gRPC headers", func(t *testing.T) {
		resp := NewInvokeMethodResponse(0, "OK", nil)
		defer resp.Close()
		md := map[string][]string{
			"test1": {"val1", "val2"},
			"test2": {"val3", "val4"},
		}
		resp.WithHeaders(md)
		mheader := resp.Headers()

		assert.Equal(t, "val1", mheader["test1"].GetValues()[0])
		assert.Equal(t, "val2", mheader["test1"].GetValues()[1])
		assert.Equal(t, "val3", mheader["test2"].GetValues()[0])
		assert.Equal(t, "val4", mheader["test2"].GetValues()[1])
	})

	t.Run("HTTP headers", func(t *testing.T) {
		resp := fasthttp.AcquireResponse()
		resp.Header.Set("Header1", "Value1")
		resp.Header.Set("Header2", "Value2")
		resp.Header.Set("Header3", "Value3")

		re := NewInvokeMethodResponse(0, "OK", nil)
		defer re.Close()
		re.WithFastHTTPHeaders(&resp.Header)
		mheader := re.Headers()

		assert.Equal(t, "Value1", mheader["Header1"].GetValues()[0])
		assert.Equal(t, "Value2", mheader["Header2"].GetValues()[0])
		assert.Equal(t, "Value3", mheader["Header3"].GetValues()[0])
	})
}

func TestResponseTrailer(t *testing.T) {
	resp := NewInvokeMethodResponse(0, "OK", nil)
	defer resp.Close()
	md := map[string][]string{
		"test1": {"val1", "val2"},
		"test2": {"val3", "val4"},
	}
	resp.WithTrailers(md)
	mheader := resp.Trailers()

	assert.Equal(t, "val1", mheader["test1"].GetValues()[0])
	assert.Equal(t, "val2", mheader["test1"].GetValues()[1])
	assert.Equal(t, "val3", mheader["test2"].GetValues()[0])
	assert.Equal(t, "val4", mheader["test2"].GetValues()[1])
}

func TestIsHTTPResponse(t *testing.T) {
	t.Run("gRPC response status", func(t *testing.T) {
		grpcResp := NewInvokeMethodResponse(int32(codes.OK), "OK", nil)
		defer grpcResp.Close()
		assert.False(t, grpcResp.IsHTTPResponse())
	})

	t.Run("HTTP response status", func(t *testing.T) {
		httpResp := NewInvokeMethodResponse(http.StatusOK, "OK", nil)
		defer httpResp.Close()
		assert.True(t, httpResp.IsHTTPResponse())
	})
}

func TestResponseReplayable(t *testing.T) {
	message := []byte("Nel mezzo del cammin di nostra vita mi ritrovai per una selva oscura, che' la diritta via era smarrita.")
	newReplayable := func() *InvokeMethodResponse {
		m := &commonv1pb.InvokeResponse{
			Data:        &anypb.Any{Value: []byte("test")},
			ContentType: "application/json",
		}
		pb := internalv1pb.InternalInvokeResponse{
			Status:  &internalv1pb.Status{Code: 0},
			Message: m,
		}

		res, _ := InternalInvokeResponse(&pb)
		return res.
			WithRawDataBytes(message, "").
			WithReplay(true)
	}

	t.Run("read once", func(t *testing.T) {
		res := newReplayable()
		defer res.Close()

		t.Run("first read in full", func(t *testing.T) {
			read, err := io.ReadAll(res.RawData())
			assert.NoError(t, err)
			assert.Equal(t, message, read)
		})

		t.Run("req.data is EOF", func(t *testing.T) {
			buf := make([]byte, 9)
			n, err := io.ReadFull(res.data, buf)
			assert.Equal(t, 0, n)
			assert.ErrorIs(t, err, io.EOF)
		})

		t.Run("replay buffer is full", func(t *testing.T) {
			assert.Equal(t, len(message), res.replay.Len())
			read, err := io.ReadAll(bytes.NewReader(res.replay.Bytes()))
			assert.NoError(t, err)
			assert.Equal(t, message, read)
		})

		t.Run("close request", func(t *testing.T) {
			err := res.Close()
			assert.NoError(t, err)
			assert.Nil(t, res.data)
			assert.Nil(t, res.replay)
		})
	})

	t.Run("read in full twice", func(t *testing.T) {
		res := newReplayable()
		defer res.Close()

		t.Run("first read in full", func(t *testing.T) {
			read, err := io.ReadAll(res.RawData())
			assert.NoError(t, err)
			assert.Equal(t, message, read)
		})

		t.Run("req.data is EOF", func(t *testing.T) {
			buf := make([]byte, 9)
			n, err := io.ReadFull(res.data, buf)
			assert.Equal(t, 0, n)
			assert.ErrorIs(t, err, io.EOF)
		})

		t.Run("replay buffer is full", func(t *testing.T) {
			assert.Equal(t, len(message), res.replay.Len())
			read, err := io.ReadAll(bytes.NewReader(res.replay.Bytes()))
			assert.NoError(t, err)
			assert.Equal(t, message, read)
		})

		t.Run("second read in full", func(t *testing.T) {
			read, err := io.ReadAll(res.RawData())
			assert.NoError(t, err)
			assert.Equal(t, message, read)
		})

		t.Run("close request", func(t *testing.T) {
			err := res.Close()
			assert.NoError(t, err)
			assert.Nil(t, res.data)
			assert.Nil(t, res.replay)
		})
	})

	t.Run("read in full, then partial read", func(t *testing.T) {
		res := newReplayable()
		defer res.Close()

		t.Run("first read in full", func(t *testing.T) {
			read, err := io.ReadAll(res.RawData())
			assert.NoError(t, err)
			assert.Equal(t, message, read)
		})

		r := res.RawData()
		t.Run("second, partial read", func(t *testing.T) {
			buf := make([]byte, 9)
			n, err := io.ReadFull(r, buf)
			assert.NoError(t, err)
			assert.Equal(t, 9, n)
			assert.Equal(t, message[:9], buf)
		})

		t.Run("read rest", func(t *testing.T) {
			read, err := io.ReadAll(r)
			assert.NoError(t, err)
			assert.Len(t, read, len(message)-9)
			// Continue from byte 9
			assert.Equal(t, message[9:], read)
		})

		t.Run("close request", func(t *testing.T) {
			err := res.Close()
			assert.NoError(t, err)
			assert.Nil(t, res.data)
			assert.Nil(t, res.replay)
		})
	})

	t.Run("partial read, then read in full", func(t *testing.T) {
		res := newReplayable()
		defer res.Close()

		t.Run("first, partial read", func(t *testing.T) {
			buf := make([]byte, 9)
			n, err := io.ReadFull(res.RawData(), buf)

			assert.NoError(t, err)
			assert.Equal(t, 9, n)
			assert.Equal(t, message[:9], buf)
		})

		t.Run("replay buffer has partial data", func(t *testing.T) {
			assert.Equal(t, 9, res.replay.Len())
			read, err := io.ReadAll(bytes.NewReader(res.replay.Bytes()))
			assert.NoError(t, err)
			assert.Equal(t, message[:9], read)
		})

		t.Run("second read in full", func(t *testing.T) {
			read, err := io.ReadAll(res.RawData())
			assert.NoError(t, err)
			assert.Equal(t, message, read)
		})

		t.Run("req.data is EOF", func(t *testing.T) {
			buf := make([]byte, 9)
			n, err := io.ReadFull(res.data, buf)
			assert.Equal(t, 0, n)
			assert.ErrorIs(t, err, io.EOF)
		})

		t.Run("replay buffer is full", func(t *testing.T) {
			assert.Equal(t, len(message), res.replay.Len())
			read, err := io.ReadAll(bytes.NewReader(res.replay.Bytes()))
			assert.NoError(t, err)
			assert.Equal(t, message, read)
		})

		t.Run("close request", func(t *testing.T) {
			err := res.Close()
			assert.NoError(t, err)
			assert.Nil(t, res.data)
			assert.Nil(t, res.replay)
		})
	})

	t.Run("get ProtoWithData twice", func(t *testing.T) {
		res := newReplayable()
		defer res.Close()

		t.Run("first ProtoWithData request", func(t *testing.T) {
			pb, err := res.ProtoWithData()
			assert.NoError(t, err)
			assert.NotNil(t, pb)
			assert.NotNil(t, pb.Message)
			assert.NotNil(t, pb.Message.Data)
			assert.Equal(t, message, pb.Message.Data.Value)
		})

		t.Run("second ProtoWithData request", func(t *testing.T) {
			pb, err := res.ProtoWithData()
			assert.NoError(t, err)
			assert.NotNil(t, pb)
			assert.NotNil(t, pb.Message)
			assert.NotNil(t, pb.Message.Data)
			assert.Equal(t, message, pb.Message.Data.Value)
		})

		t.Run("close request", func(t *testing.T) {
			err := res.Close()
			assert.NoError(t, err)
			assert.Nil(t, res.data)
			assert.Nil(t, res.replay)
		})
	})
}
