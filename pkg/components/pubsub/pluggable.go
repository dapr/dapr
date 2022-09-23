/*
Copyright 2022 The Dapr Authors
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
	"io"
	"sync"

	"github.com/dapr/components-contrib/pubsub"

	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/components/pluggable"
	proto "github.com/dapr/dapr/pkg/proto/components/v1"

	"github.com/dapr/kit/logger"

	"github.com/pkg/errors"
)

// grpcPubSub is a implementation of a pubsub over a gRPC Protocol.
type grpcPubSub struct {
	*pluggable.GRPCConnector[proto.PubSubClient]
	// features the list of state store implemented features features.
	features []pubsub.Feature
	logger   logger.Logger
}

// Init initializes the grpc pubsub passing out the metadata to the grpc component.
// It also fetches and set the component features.
func (p *grpcPubSub) Init(metadata pubsub.Metadata) error {
	if err := p.Dial(metadata.Name); err != nil {
		return err
	}

	protoMetadata := &proto.MetadataRequest{
		Properties: metadata.Properties,
	}

	_, err := p.Client.Init(p.Context, &proto.PubSubInitRequest{
		Metadata: protoMetadata,
	})
	if err != nil {
		return err
	}

	// TODO Static data could be retrieved in another way, a necessary discussion should start soon.
	// we need to call the method here because features could return an error and the features interface doesn't support errors
	featureResponse, err := p.Client.Features(p.Context, &proto.FeaturesRequest{})
	if err != nil {
		return err
	}

	p.features = make([]pubsub.Feature, len(featureResponse.Feature))
	for idx, f := range featureResponse.Feature {
		p.features[idx] = pubsub.Feature(f)
	}

	return nil
}

// Features lists all implemented features.
func (p *grpcPubSub) Features() []pubsub.Feature {
	return p.features
}

// Publish publishes data to a topic.
func (p *grpcPubSub) Publish(req *pubsub.PublishRequest) error {
	_, err := p.Client.Publish(p.Context, &proto.PublishRequest{
		Topic:      req.Topic,
		PubsubName: req.PubsubName,
		Data:       req.Data,
		Metadata:   req.Metadata,
	})
	return err
}

type messageHandler = func(*proto.PullMessagesResponse)

// adaptHandler returns a non-error function that handle the message with the given handler and ack when returns.
//
//nolint:nosnakecase
func (p *grpcPubSub) adaptHandler(ctx context.Context, streamingPull proto.PubSub_PullMessagesClient, handler pubsub.Handler) messageHandler {
	safeSend := &sync.Mutex{}
	return func(msg *proto.PullMessagesResponse) {
		m := pubsub.NewMessage{
			Data:        msg.Data,
			ContentType: &msg.ContentType,
			Topic:       msg.TopicName,
			Metadata:    msg.Metadata,
		}
		var ackError *proto.AckMessageError

		if err := handler(ctx, &m); err != nil {
			p.logger.Errorf("error when handling message on topic %s", msg.TopicName)
			ackError = &proto.AckMessageError{
				Message: err.Error(),
			}
		}

		// As per documentation:
		// When using streams,
		// one must take care to avoid calling either SendMsg or RecvMsg multiple times against the same Stream from different goroutines.
		// In other words, it's safe to have a goroutine calling SendMsg and another goroutine calling RecvMsg on the same stream at the same time.
		// But it is not safe to call SendMsg on the same stream in different goroutines, or to call RecvMsg on the same stream in different goroutines.
		// https://github.com/grpc/grpc-go/blob/master/Documentation/concurrency.md#streams
		safeSend.Lock()
		defer safeSend.Unlock()

		if err := streamingPull.Send(&proto.PullMessagesRequest{
			AckMessageId: msg.Id,
			AckError:     ackError,
		}); err != nil {
			p.logger.Errorf("error when ack'ing message %s from topic %s", msg.Id, msg.TopicName)
		}
	}
}

// pullMessages pull messages of the given subscription and execute the handler for that messages.
func (p *grpcPubSub) pullMessages(ctx context.Context, topic *proto.Topic, handler pubsub.Handler) error {
	// first pull should be sync and subsequent connections can be made in background if necessary
	pull, err := p.Client.PullMessages(ctx)
	if err != nil {
		return errors.Wrapf(err, "unable to subscribe")
	}

	streamCtx, cancel := context.WithCancel(pull.Context())

	err = pull.Send(&proto.PullMessagesRequest{
		Topic: topic,
	})

	cleanup := func() {
		if closeErr := pull.CloseSend(); closeErr != nil {
			p.logger.Warnf("could not close pull stream of topic %s: %v", topic.Name, closeErr)
		}
		cancel()
	}

	if err != nil {
		cleanup()
		return errors.Wrapf(err, "unable to subscribe")
	}

	handle := p.adaptHandler(streamCtx, pull, handler)
	go func() {
		defer cleanup()
		for {
			msg, err := pull.Recv()
			if err == io.EOF { // no more messages
				return
			}

			// TODO reconnect on error
			if err != nil {
				p.logger.Errorf("failed to receive message: %v", err)
				return
			}

			p.logger.Debugf("received message from stream on topic %s", msg.TopicName)

			go handle(msg)
		}
	}()

	return nil
}

// Subscribe subscribes to a given topic and callback the handler when a new message arrives.
func (p *grpcPubSub) Subscribe(ctx context.Context, req pubsub.SubscribeRequest, handler pubsub.Handler) error {
	subscription := &proto.Topic{
		Name:     req.Topic,
		Metadata: req.Metadata,
	}
	return p.pullMessages(ctx, subscription, handler)
}

// fromConnector creates a new GRPC pubsub using the given underlying connector.
func fromConnector(l logger.Logger, connector *pluggable.GRPCConnector[proto.PubSubClient]) *grpcPubSub {
	return &grpcPubSub{
		features:      make([]pubsub.Feature, 0),
		GRPCConnector: connector,
		logger:        l,
	}
}

// NewGRPCPubSub creates a new grpc pubsub using the given socket factory.
func NewGRPCPubSub(l logger.Logger, socketFactory func(string) string) *grpcPubSub {
	return fromConnector(l, pluggable.NewGRPCConnectorWithFactory(socketFactory, proto.NewPubSubClient))
}

// newGRPCPubSub creates a new grpc pubsub for the given pluggable component.
func newGRPCPubSub(l logger.Logger, pc components.Pluggable) pubsub.PubSub {
	return fromConnector(l, pluggable.NewGRPCConnector(pc, proto.NewPubSubClient))
}

func init() {
	pluggable.AddRegistryFor(components.PubSub, DefaultRegistry.RegisterComponent, newGRPCPubSub)
}
