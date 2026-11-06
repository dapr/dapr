/*
Copyright 2025 The Dapr Authors
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

package sse

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

func TestIsSSEHttpRequest(t *testing.T) {
	tests := []struct {
		name   string
		accept string
		want   bool
	}{
		{"exact match", "text/event-stream", true},
		{"case insensitive", "Text/Event-Stream", true},
		{"surrounding whitespace", "  text/event-stream  ", true},
		{"empty", "", false},
		{"different value", "application/json", false},
		{"prefix only, not exact", "text/event-stream; charset=utf-8", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.accept != "" {
				r.Header.Set("Accept", tt.accept)
			}
			assert.Equal(t, tt.want, IsSSEHttpRequest(r))
		})
	}
}

func TestIsSSEGrpcRequest(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]*internalv1pb.ListStringValue
		want     bool
	}{
		{
			name: "accept header present",
			metadata: map[string]*internalv1pb.ListStringValue{
				"Accept": {Values: []string{"text/event-stream"}},
			},
			want: true,
		},
		{
			name: "accept header wrong value",
			metadata: map[string]*internalv1pb.ListStringValue{
				"Accept": {Values: []string{"application/json"}},
			},
			want: false,
		},
		{
			name:     "no metadata",
			metadata: nil,
			want:     false,
		},
		{
			name: "no accept key",
			metadata: map[string]*internalv1pb.ListStringValue{
				"Content-Type": {Values: []string{"text/event-stream"}},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &internalv1pb.InternalInvokeRequest{Metadata: tt.metadata}
			assert.Equal(t, tt.want, IsSSEGrpcRequest(req))
		})
	}
}

func TestHandleSSEGrpcResponse(t *testing.T) {
	t.Run("nil response returns nil error", func(t *testing.T) {
		assert.NoError(t, HandleSSEGrpcResponse(nil))
	})

	t.Run("status ok still returns error with unexpected-response message", func(t *testing.T) {
		res := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		err := HandleSSEGrpcResponse(res)
		require.Error(t, err)
		assert.Equal(t, codes.Internal, status.Code(err))
		assert.Contains(t, err.Error(), "no expected response from stream")
	})

	t.Run("status not ok returns error with no-response message", func(t *testing.T) {
		res := invokev1.NewInvokeMethodResponse(500, "Internal Server Error", nil)
		err := HandleSSEGrpcResponse(res)
		require.Error(t, err)
		assert.Equal(t, codes.Internal, status.Code(err))
		assert.Contains(t, err.Error(), "no response received from stream")
	})
}

func TestAddSSEHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	w.Header().Set("Content-Length", "100")

	AddSSEHeaders(w)

	assert.Equal(t, mimeEventStream, w.Header().Get(headerContentType))
	assert.Equal(t, cacheNoCache, w.Header().Get(headerCacheControl))
	assert.Equal(t, connectionKeepAlive, w.Header().Get(headerConnection))
	assert.Empty(t, w.Header().Get(headerContentLength))
}

// nonFlushingWriter implements http.ResponseWriter but deliberately not
// http.Flusher (unlike httptest.ResponseRecorder, which implements Flush).
type nonFlushingWriter struct {
	header     http.Header
	statusCode int
	body       bytes.Buffer
}

func newNonFlushingWriter() *nonFlushingWriter {
	return &nonFlushingWriter{header: http.Header{}}
}

func (w *nonFlushingWriter) Header() http.Header { return w.header }

func (w *nonFlushingWriter) Write(p []byte) (int, error) { return w.body.Write(p) }

func (w *nonFlushingWriter) WriteHeader(statusCode int) { w.statusCode = statusCode }

func TestFlushSSEResponse(t *testing.T) {
	t.Run("writer without Flusher support returns nil after writing 500", func(t *testing.T) {
		w := newNonFlushingWriter()
		err := FlushSSEResponse(context.Background(), w, bytes.NewBufferString("data: hello\n\n"))
		require.NoError(t, err)
		assert.Equal(t, http.StatusInternalServerError, w.statusCode)
	})

	t.Run("streams all data until EOF", func(t *testing.T) {
		w := httptest.NewRecorder()
		payload := "data: hello\n\ndata: world\n\n"
		err := FlushSSEResponse(context.Background(), w, bytes.NewBufferString(payload))
		require.NoError(t, err)
		assert.Equal(t, payload, w.Body.String())
	})

	t.Run("context already canceled returns context error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		w := httptest.NewRecorder()
		err := FlushSSEResponse(ctx, w, bytes.NewBufferString("data: hello\n\n"))
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("reader error other than EOF is propagated", func(t *testing.T) {
		w := httptest.NewRecorder()
		wantErr := errors.New("boom")
		err := FlushSSEResponse(context.Background(), w, &erroringReader{err: wantErr})
		require.Error(t, err)
		assert.ErrorIs(t, err, wantErr)
	})

	t.Run("closes reader when it implements io.Closer", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := &closingReader{Buffer: bytes.NewBufferString("data: hello\n\n")}
		err := FlushSSEResponse(context.Background(), w, r)
		require.NoError(t, err)
		assert.True(t, r.closed)
	})
}

type erroringReader struct {
	err error
}

func (e *erroringReader) Read([]byte) (int, error) {
	return 0, e.err
}

type closingReader struct {
	*bytes.Buffer
	closed bool
}

func (c *closingReader) Read(p []byte) (int, error) {
	return c.Buffer.Read(p)
}

func (c *closingReader) Close() error {
	c.closed = true
	return nil
}

var _ io.Reader = (*erroringReader)(nil)
