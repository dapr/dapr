// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package nats

import (
	"errors"
	"fmt"

	log "github.com/Sirupsen/logrus"
	"github.com/dapr/components-contrib/pubsub"
	nats "github.com/nats-io/go-nats"
)

const (
	natsURL    = "natsURL"
	consumerID = "consumerID"
)

type natsPubSub struct {
	metadata metadata
	natsConn *nats.Conn
}

// NewNATSPubSub returns a new NATS pub-sub implementation
func NewNATSPubSub() pubsub.PubSub {
	return &natsPubSub{}
}

func parseNATSMetadata(meta pubsub.Metadata) (metadata, error) {
	m := metadata{}
	if val, ok := meta.Properties[natsURL]; ok && val != "" {
		m.natsURL = val
	} else {
		return m, errors.New("nats error: missing nats URL")
	}

	if val, ok := meta.Properties[consumerID]; ok && val != "" {
		m.natsQueueGroupName = val
	} else {
		return m, errors.New("nats error: missing queue name")
	}

	return m, nil
}

func (n *natsPubSub) Init(metadata pubsub.Metadata) error {
	m, err := parseNATSMetadata(metadata)
	if err != nil {
		return err
	}

	n.metadata = m
	natsConn, err := nats.Connect(m.natsURL)
	if err != nil {
		return fmt.Errorf("nats: error connecting to nats at %s: %s", m.natsURL, err)
	}
	log.Debugf("connected to nats at %s", m.natsURL)

	n.natsConn = natsConn
	return nil
}

func (n *natsPubSub) Publish(req *pubsub.PublishRequest) error {
	err := n.natsConn.Publish(req.Topic, req.Data)
	if err != nil {
		return fmt.Errorf("nats: error from publish: %s", err)
	}
	return nil
}

func (n *natsPubSub) Subscribe(req pubsub.SubscribeRequest, handler func(msg *pubsub.NewMessage) error) error {
	sub, err := n.natsConn.QueueSubscribe(req.Topic, n.metadata.natsQueueGroupName, func(natsMsg *nats.Msg) {
		handler(&pubsub.NewMessage{Topic: req.Topic, Data: natsMsg.Data})
	})
	if err != nil {
		log.Warnf("nats: error subscribe: %s", err)
	}
	log.Debugf("nats: subscribed to subject %s with queue group %s", sub.Subject, sub.Queue)

	return nil
}
