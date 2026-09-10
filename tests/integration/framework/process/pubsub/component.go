/*
Copyright 2023 The Dapr Authors
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

package pubsub

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/pubsub"
	compv1pb "github.com/dapr/dapr/pkg/proto/components/v1"
)

// component is an implementation of the pubsub pluggable component
// interface.
type component struct {
	impl      pubsub.PubSub
	pmrReqCh  chan<- *compv1pb.PullMessagesRequest
	pmrRespCh <-chan *compv1pb.PullMessagesResponse
}

func newComponent(t *testing.T, opts options) *component {
	return &component{
		impl:      opts.pubsub,
		pmrReqCh:  opts.pmrReqCh,
		pmrRespCh: opts.pmrRespCh,
	}
}

func (c *component) Init(ctx context.Context, req *compv1pb.PubSubInitRequest) (*compv1pb.PubSubInitResponse, error) {
	return new(compv1pb.PubSubInitResponse), c.impl.Init(ctx, pubsub.Metadata{
		Base: metadata.Base{
			Name:       "pubsub.in-memory",
			Properties: req.GetMetadata().GetProperties(),
		},
	})
}

func (c *component) Features(context.Context, *compv1pb.FeaturesRequest) (*compv1pb.FeaturesResponse, error) {
	implF := c.impl.Features()
	features := make([]string, len(implF))
	for i, f := range implF {
		features[i] = string(f)
	}
	return &compv1pb.FeaturesResponse{
		Features: features,
	}, nil
}

func (c *component) Publish(ctx context.Context, req *compv1pb.PublishRequest) (*compv1pb.PublishResponse, error) {
	var contentType *string
	if len(req.GetContentType()) != 0 {
		contentType = &req.ContentType
	}

	err := c.impl.Publish(ctx, &pubsub.PublishRequest{
		Data:        req.GetData(),
		PubsubName:  req.GetPubsubName(),
		Topic:       req.GetTopic(),
		Metadata:    req.GetMetadata(),
		ContentType: contentType,
	})
	if err != nil {
		return nil, err
	}

	return new(compv1pb.PublishResponse), nil
}

func (c *component) BulkPublish(ctx context.Context, req *compv1pb.BulkPublishRequest) (*compv1pb.BulkPublishResponse, error) {
	// TODO:
	return new(compv1pb.BulkPublishResponse), nil
}

func (c *component) PullMessages(req compv1pb.PubSub_PullMessagesServer) error {
	_, err := req.Recv()
	if err != nil {
		return err
	}

	// Recv runs in its own goroutine so that acks are consumed independently
	// of the Send loop. Blocking the Send loop on the ack of the previous
	// message would serialise delivery at the broker and mask whether the
	// runtime dispatches messages concurrently. Per gRPC docs, Send and Recv
	// on the same stream from separate goroutines is safe.
	recvErr := make(chan error, 1)
	go func() {
		for {
			resp, err := req.Recv()
			if err != nil {
				recvErr <- err
				return
			}
			select {
			case c.pmrReqCh <- resp:
			case <-req.Context().Done():
				return
			}
		}
	}()

	for {
		select {
		case pmr := <-c.pmrRespCh:
			if err := req.Send(pmr); err != nil {
				return err
			}
		case err := <-recvErr:
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		case <-req.Context().Done():
			return nil
		}
	}
}

func (c *component) Ping(ctx context.Context, req *compv1pb.PingRequest) (*compv1pb.PingResponse, error) {
	return new(compv1pb.PingResponse), nil
}

func (c *component) Close() error {
	return c.impl.(io.Closer).Close()
}
