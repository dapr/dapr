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
	features  []pubsub.Feature
	logger    logger.Logger
	ackStream proto.PubSub_AckMessageClient //nolint:nosnakecase
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

	_, err = p.Client.Init(p.Context, &proto.PubSubInitRequest{
		Metadata: protoMetadata,
	})

	if err != nil {
		return err
	}

	ackStream, err := p.Client.AckMessage(p.Context)
	p.ackStream = ackStream
	return err
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

// handleAndAck handles the message and ack in case of success or failure.
func (p *grpcPubSub) handleAndAck(ctx context.Context, msg *proto.Message, handler pubsub.Handler) {
	m := pubsub.NewMessage{
		Data:        msg.Data,
		ContentType: &msg.ContentType,
		Topic:       msg.Topic,
		Metadata:    msg.Metadata,
	}

	var ackError *proto.AckMessageError

	if err := handler(ctx, &m); err != nil {
		p.logger.Errorf("error when handling message on topic %s", msg.Topic)
		ackError = &proto.AckMessageError{
			Message: err.Error(),
		}
	}

	if err := p.ackStream.Send(&proto.AckMessageRequest{
		MessageId: msg.Id,
		Error:     ackError,
	}); err != nil {
		p.logger.Errorf("error when ack'ing message %s from topic %s", msg.Id, msg.Topic)
	}
}

// Subscribe subscribes to a given topic and callback the handler when a new message arrives.
func (p *grpcPubSub) Subscribe(ctx context.Context, req pubsub.SubscribeRequest, handler pubsub.Handler) error {
	protoReq := proto.SubscribeRequest{
		Topic:    req.Topic,
		Metadata: req.Metadata,
	}
	msgStream, err := p.Client.Subscribe(ctx, &protoReq)
	if err != nil {
		return errors.Wrapf(err, "unable to subscribe")
	}
	streamCtx := msgStream.Context()
	go func() {
		for {
			resp, err := msgStream.Recv()
			if err == io.EOF { // no more messages
				return
			}

			if err != nil {
				p.logger.Errorf("failed to receive message: %v", err)
				return
			}

			p.logger.Debugf("received message from stream on topic %s", resp.Topic)

			go func(message *proto.Message) {
				p.handleAndAck(streamCtx, message, handler)
			}(resp)
		}
	}()

	return nil
}

func (p *grpcPubSub) Close() error {
	return p.GRPCConnector.Close()
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
