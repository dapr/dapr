// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package rabbitmq

import (
	"encoding/json"

	"github.com/dapr/components-contrib/bindings"
	"github.com/streadway/amqp"
)

// RabbitMQ allows sending/receiving data to/from RabbitMQ
type RabbitMQ struct {
	connection *amqp.Connection
	channel    *amqp.Channel
	metadata   *rabbitMQMetadata
}

// Metadata is the rabbitmq config
type rabbitMQMetadata struct {
	QueueName        string `json:"queueName"`
	Host             string `json:"host"`
	Durable          bool   `json:"durable,string"`
	DeleteWhenUnused bool   `json:"deleteWhenUnused,string"`
}

// NewRabbitMQ returns a new rabbitmq instance
func NewRabbitMQ() *RabbitMQ {
	return &RabbitMQ{}
}

// Init does metadata parsing and connection creation
func (r *RabbitMQ) Init(metadata bindings.Metadata) error {
	meta, err := r.getRabbitMQMetadata(metadata)
	if err != nil {
		return err
	}

	r.metadata = meta

	conn, err := amqp.Dial(meta.Host)
	if err != nil {
		return err
	}

	ch, err := conn.Channel()
	if err != nil {
		return err
	}

	r.connection = conn
	r.channel = ch
	return nil
}

func (r *RabbitMQ) Write(req *bindings.WriteRequest) error {
	err := r.channel.Publish("", r.metadata.QueueName, false, false, amqp.Publishing{
		ContentType: "text/plain",
		Body:        req.Data,
	})
	if err != nil {
		return err
	}
	return nil
}

func (r *RabbitMQ) getRabbitMQMetadata(metadata bindings.Metadata) (*rabbitMQMetadata, error) {
	b, err := json.Marshal(metadata.Properties)
	if err != nil {
		return nil, err
	}

	var rabbitMQMeta rabbitMQMetadata
	err = json.Unmarshal(b, &rabbitMQMeta)
	if err != nil {
		return nil, err
	}
	return &rabbitMQMeta, nil
}

func (r *RabbitMQ) Read(handler func(*bindings.ReadResponse) error) error {
	q, err := r.channel.QueueDeclare(r.metadata.QueueName, r.metadata.Durable, r.metadata.DeleteWhenUnused, false, false, nil)
	if err != nil {
		return err
	}

	msgs, err := r.channel.Consume(
		q.Name,
		"",
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return err
	}

	forever := make(chan bool)

	go func() {
		for d := range msgs {
			err := handler(&bindings.ReadResponse{
				Data: d.Body,
			})
			if err == nil {
				r.channel.Ack(d.DeliveryTag, false)
			}
		}
	}()

	<-forever
	return nil
}
