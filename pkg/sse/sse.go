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
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/messages"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

const (
	headerContentType  = "Content-Type"
	headerCacheControl = "Cache-Control"
	headerConnection   = "Connection"

	mimeEventStream     = "text/event-stream"
	cacheNoCache        = "no-cache"
	connectionKeepAlive = "keep-alive"
)

func IsSSEHttpRequest(r *http.Request) bool {
	return isSSE(&r.Header)
}

func IsSSEGrpcRequest(r *internalv1pb.InternalInvokeRequest) (bool, http.Header) {
	header := http.Header{}
	for k, v := range r.GetMetadata() {
		header[k] = v.GetValues()
	}

	return isSSE(&header), header
}

func isSSE(header *http.Header) bool {
	accept := header.Get("Accept")
	return strings.EqualFold(strings.TrimSpace(accept), "text/event-stream")
}

func HandleSSEGrpcResponse(res *invokev1.InvokeMethodResponse) error {
	if res == nil {
		return nil
	}

	statusOK := res.Status().GetCode() >= 200 && res.Status().GetCode() < 300
	msg := "no response received from stream"
	if statusOK {
		msg = "no expected response from stream"
	}

	return status.Errorf(codes.Internal, messages.ErrChannelInvoke, errors.New(msg))
}

func FlushSSEResponse(ctx context.Context, writer http.ResponseWriter, reader io.Reader) error {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		http.Error(writer, "Streaming unsupported!", http.StatusInternalServerError)
		return nil
	}

	writer.Header().Set(headerContentType, mimeEventStream)
	writer.Header().Set(headerCacheControl, cacheNoCache)
	writer.Header().Set(headerConnection, connectionKeepAlive)

	// Add defer close for streaming case
	closer, ok := reader.(io.Closer)
	if ok {
		defer closer.Close()
	}

	// Stream SSE data in real-time
	buf := make([]byte, 1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		n, err := reader.Read(buf)
		if n > 0 {
			_, err = writer.Write(buf[:n])
			if err != nil {
				// Client disconnected - break immediately
				return err
			}
			// Flush immediately for SSE
			flusher.Flush()
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}
