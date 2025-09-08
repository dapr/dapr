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

package binding

import (
	"context"
	"testing"

	compv1pb "github.com/dapr/dapr/pkg/proto/components/v1"
)

type component struct {
	msgCh  chan *compv1pb.ReadResponse
	respCh chan *compv1pb.ReadRequest
}

func newComponent(t *testing.T, opts options) *component {
	t.Helper()

	return &component{
		msgCh:  make(chan *compv1pb.ReadResponse, 1),
		respCh: make(chan *compv1pb.ReadRequest, 1),
	}
}

func (c *component) Init(ctx context.Context, req *compv1pb.InputBindingInitRequest) (*compv1pb.InputBindingInitResponse, error) {
	return new(compv1pb.InputBindingInitResponse), nil
}

func (c *component) Read(stream compv1pb.InputBinding_ReadServer) error {
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case msg := <-c.msgCh:
			if err := stream.Send(msg); err != nil {
				return err
			}

			resp, err := stream.Recv()
			if err != nil {
				return err
			}

			c.respCh <- resp
		}
	}
}

func (c *component) Ping(context.Context, *compv1pb.PingRequest) (*compv1pb.PingResponse, error) {
	return new(compv1pb.PingResponse), nil
}
