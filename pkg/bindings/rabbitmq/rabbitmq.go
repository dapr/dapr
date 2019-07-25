package rabbitmq

import (
	"encoding/json"

	"github.com/actionscore/actions/pkg/components/bindings"
	"github.com/streadway/amqp"
)

// RabbitMQ allows sending/receving data to/from RabbitMQ
type RabbitMQ struct {
	connection *amqp.Connection
	channel    *amqp.Channel
	metadata   *Metadata
}

// Metadata is the rabbitmq config
type Metadata struct {
	QueueName        string `json:"queueName"`
	Host             string `json:"host"`
	Durable          bool   `json:"durable"`
	DeleteWhenUnused bool   `json:"deleteWhenUnused"`
}

// NewRabbitMQ returns a new rabbitmq instance
func NewRabbitMQ() *RabbitMQ {
	return &RabbitMQ{}
}

// Init does metadata parsing and connection creation
func (r *RabbitMQ) Init(metadata bindings.Metadata) error {
	meta, err := r.GetRabbitMQMetadata(metadata)
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
	q, err := r.channel.QueueDeclare(r.metadata.QueueName, r.metadata.Durable, r.metadata.DeleteWhenUnused, false, false, nil)
	if err != nil {
		return err
	}

	err = r.channel.Publish("", q.Name, false, false, amqp.Publishing{
		ContentType: "text/plain",
		Body:        req.Data,
	})
	if err != nil {
		return err
	}

	return nil
}

// GetRabbitMQMetadata gets rabbitmq metadata
func (r *RabbitMQ) GetRabbitMQMetadata(metadata bindings.Metadata) (*Metadata, error) {
	b, err := json.Marshal(metadata.ConnectionInfo)
	if err != nil {
		return nil, err
	}

	var rabbitMQMeta Metadata
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
		true,
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
			if len(d.Body) > 0 {
				err := handler(&bindings.ReadResponse{
					Data: d.Body,
				})
				if err == nil {
					r.channel.Ack(d.DeliveryTag, false)
				}
			}
		}
	}()

	<-forever
	return nil
}
