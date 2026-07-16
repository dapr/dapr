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

package grpc

import (
	"net/http"

	"google.golang.org/grpc/codes"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/kit/logger"
)

type streamResponseWriter struct {
	stream internalv1pb.ServiceInvocation_CallLocalStreamServer
	header http.Header
	buf    []byte
	seq    uint64
	status int
	appID  string
	logger logger.Logger
}

func (lrw *streamResponseWriter) Write(b []byte) (int, error) {
	lrw.buf = append(lrw.buf, b...)
	return len(b), nil
}

func (lrw *streamResponseWriter) WriteHeader(statusCode int) {
	lrw.status = statusCode
}

func (lrw *streamResponseWriter) Flush() {
	r := &internalv1pb.InternalInvokeResponse{}

	if lrw.seq == 0 {
		r.Headers = internalv1pb.HTTPHeadersToInternalMetadata(lrw.header)
		r.Status = &internalv1pb.Status{
			Code: int32(codes.OK),
		}
	}

	err := lrw.stream.Send(&internalv1pb.InternalInvokeResponseStream{
		Response: r,
		Payload: &commonv1pb.StreamPayload{
			Data: lrw.buf,
			Seq:  lrw.seq,
		},
	})
	if err != nil {
		lrw.logger.Errorf("Stream flush error: %s", err)
	}

	lrw.buf = lrw.buf[:0]
	lrw.seq++
	lrw.status = 0
}

func (lrw *streamResponseWriter) Header() http.Header {
	return lrw.header
}
