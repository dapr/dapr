package action

import (
	"encoding/json"

	"github.com/streadway/amqp"
)

type RabbitMQ struct {
	connection *amqp.Connection
	channel    *amqp.Channel
	metadata   *RabbitMQMetadata
}

type RabbitMQMetadata struct {
	QueueName        string `json:"queueName"`
	Host             string `json:"host"`
	Durable          bool   `json:"durable"`
	DeleteWhenUnused bool   `json:"deleteWhenUnused"`
}

func NewRabbitMQ() *RabbitMQ {
	return &RabbitMQ{}
}

func (r *RabbitMQ) Init(eventSourceSpec EventSourceSpec) error {
	meta, err := r.GetRabbitMQMetadata(eventSourceSpec)
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

func (r *RabbitMQ) Write(data interface{}) error {
	q, err := r.channel.QueueDeclare(r.metadata.QueueName, r.metadata.Durable, r.metadata.DeleteWhenUnused, false, false, nil)
	if err != nil {
		return err
	}

	b, err := json.Marshal(data)
	if err != nil {
		return err
	}

	err = r.channel.Publish("", q.Name, false, false, amqp.Publishing{
		ContentType: "text/plain",
		Body:        b,
	})
	if err != nil {
		return err
	}

	return nil
}

func (r *RabbitMQ) Read(metadata interface{}) (interface{}, error) {
	return nil, nil
}

func (r *RabbitMQ) GetRabbitMQMetadata(eventSourceSpec EventSourceSpec) (*RabbitMQMetadata, error) {
	b, err := json.Marshal(eventSourceSpec.ConnectionInfo)
	if err != nil {
		return nil, err
	}

	var metadata RabbitMQMetadata
	err = json.Unmarshal(b, &metadata)
	if err != nil {
		return nil, err
	}

	return &metadata, nil
}

func (r *RabbitMQ) ReadAsync(metadata interface{}, callback func([]byte) error) error {
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
			callback(d.Body)
		}
	}()

	<-forever
	return nil
}
