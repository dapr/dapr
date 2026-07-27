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
	"sync"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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

	// paused gates PullMessages from forwarding new messages from
	// pmrRespCh while the component is paused — faithfully simulating
	// Kafka's PauseAll semantics for integration tests of the runtime's
	// pause-and-drain shutdown path.
	paused        atomic.Bool
	pauseCalled   atomic.Int64
	resumeCalled  atomic.Int64
	pauseStartCh  chan struct{}
	pauseStartOnc sync.Once
	// pausable opts in to Pause/Resume; default is non-pausable
	// (returns codes.Unimplemented), matching most pubsub components.
	pausable bool
}

func newComponent(t *testing.T, opts options) *component {
	return &component{
		impl:         opts.pubsub,
		pmrReqCh:     opts.pmrReqCh,
		pmrRespCh:    opts.pmrRespCh,
		pauseStartCh: make(chan struct{}),
		pausable:     opts.pausable,
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

	// Recv runs in its own goroutine so acks for already-delivered
	// messages keep flowing while the Send loop is paused. Per gRPC
	// docs, Send and Recv on the same stream from separate goroutines
	// is safe.
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

	// Send loop. Once Pause is called pauseStartCh is closed and the
	// select fires that case immediately, dropping into a paused wait
	// where no further pmrRespCh messages are forwarded. Selecting on a
	// channel (rather than re-checking the atomic each iteration)
	// closes the race where Pause fires after respCh is already set.
	for {
		select {
		case pmr := <-c.pmrRespCh:
			if err := req.Send(pmr); err != nil {
				return err
			}
		case <-c.pauseStartCh:
			return c.waitForStreamEnd(req, recvErr)
		case err := <-recvErr:
			return cleanShutdown(err)
		case <-req.Context().Done():
			return nil
		}
	}
}

// waitForStreamEnd is the paused state — no more pmrRespCh forwards;
// just wait for the stream to terminate.
func (c *component) waitForStreamEnd(req compv1pb.PubSub_PullMessagesServer, recvErr <-chan error) error {
	select {
	case err := <-recvErr:
		return cleanShutdown(err)
	case <-req.Context().Done():
		return nil
	}
}

// cleanShutdown collapses io.EOF / context.Canceled into a nil return
// (normal stream end) while letting other errors surface.
func cleanShutdown(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func (c *component) Pause(ctx context.Context, req *compv1pb.PauseRequest) (*compv1pb.PauseResponse, error) {
	c.pauseCalled.Add(1)
	if !c.pausable {
		return nil, status.Error(codes.Unimplemented, "pause not implemented")
	}
	c.paused.Store(true)
	c.pauseStartOnc.Do(func() { close(c.pauseStartCh) })
	return new(compv1pb.PauseResponse), nil
}

func (c *component) Resume(ctx context.Context, req *compv1pb.ResumeRequest) (*compv1pb.ResumeResponse, error) {
	c.resumeCalled.Add(1)
	if !c.pausable {
		return nil, status.Error(codes.Unimplemented, "resume not implemented")
	}
	c.paused.Store(false)
	return new(compv1pb.ResumeResponse), nil
}

func (c *component) Ping(ctx context.Context, req *compv1pb.PingRequest) (*compv1pb.PingResponse, error) {
	return new(compv1pb.PingResponse), nil
}

func (c *component) Close() error {
	return c.impl.(io.Closer).Close()
}
