package rabbitmq

import (
	"fmt"

	"github.com/dapr/components-contrib/pubsub"
	log "github.com/sirupsen/logrus"
	"github.com/streadway/amqp"
)

const (
	fanoutExchangeKind = "fanout"
	logMessagePrefix   = "rabbitmq pub/sub:"
	errorMessagePrefix = "rabbitmq pub/sub error:"

	metadataHostKey             = "host"
	metadataConsumerIDKey       = "consumerID"
	metadataDurableKey          = "durable"
	metadataDeleteWhenUnusedKey = "deletedWhenUnused"
	metadataAutoAckKey          = "autoAck"
	metadataDeliveryModeKey     = "deliveryMode"
	metadataRequeueInFailureKey = "requeueInFailure"
)

// RabbitMQ allows sending/receiving messages in pub/sub format
type rabbitMQ struct {
	connection        *amqp.Connection
	channel           *amqp.Channel
	metadata          *metadata
	declaredExchanges map[string]bool
}

// NewRabbitMQ creates a new RabbitMQ pub/sub
func NewRabbitMQ() pubsub.PubSub {
	return createRabbitMQ()
}

func createRabbitMQ() *rabbitMQ {
	return &rabbitMQ{declaredExchanges: make(map[string]bool)}
}

// Init does metadata parsing and connection creation
func (r *rabbitMQ) Init(metadata pubsub.Metadata) error {
	meta, err := createMetadata(metadata)
	if err != nil {
		return err
	}

	r.metadata = meta

	conn, err := amqp.Dial(meta.host)
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

func (r *rabbitMQ) Publish(req *pubsub.PublishRequest) error {
	err := r.ensureExchangeDeclared(req.Topic)
	if err != nil {
		return err
	}

	log.Debugf("%s publishing message to topic '%s'", logMessagePrefix, req.Topic)

	err = r.channel.Publish(req.Topic, "", false, false, amqp.Publishing{
		ContentType:  "text/plain",
		Body:         req.Data,
		DeliveryMode: r.metadata.deliveryMode,
	})

	if err != nil {
		return err
	}

	return nil
}

func (r *rabbitMQ) Subscribe(req pubsub.SubscribeRequest, handler func(msg *pubsub.NewMessage) error) error {
	err := r.ensureExchangeDeclared(req.Topic)
	if err != nil {
		return err
	}

	queueName := fmt.Sprintf("%s-%s", r.metadata.consumerID, req.Topic)

	log.Debugf("%s declaring queue '%s'", logMessagePrefix, queueName)
	q, err := r.channel.QueueDeclare(queueName, r.metadata.durable, r.metadata.deleteWhenUnused, true, false, nil)
	if err != nil {
		return err
	}

	log.Debugf("%s binding queue '%s' to exchange '%s'", logMessagePrefix, q.Name, req.Topic)
	err = r.channel.QueueBind(q.Name, "", req.Topic, false, nil)
	if err != nil {
		return err
	}

	msgs, err := r.channel.Consume(
		q.Name,
		queueName,           // consumerId
		r.metadata.autoAck,  // autoAck
		!r.metadata.durable, // exclusive
		false,               // noLocal
		false,               // noWait
		nil,
	)

	if err != nil {
		return err
	}

	go r.listenMessages(msgs, req.Topic, handler)

	return nil
}

func (r *rabbitMQ) listenMessages(msgs <-chan amqp.Delivery, topic string, handler func(msg *pubsub.NewMessage) error) {
	for d := range msgs {
		r.handleMessage(d, topic, handler)
	}
}

func (r *rabbitMQ) handleMessage(d amqp.Delivery, topic string, handler func(msg *pubsub.NewMessage) error) {
	pubsubMsg := &pubsub.NewMessage{
		Data:  d.Body,
		Topic: topic,
	}

	err := handler(pubsubMsg)
	if err != nil {
		log.Errorf("%s error handling message from topic '%s', %s", logMessagePrefix, topic, err)
	}

	// if message is not auto acked we need to ack/nack
	if !r.metadata.autoAck {
		if err != nil {
			requeue := r.metadata.requeueInFailure && !d.Redelivered

			log.Debugf("%s nacking message '%s' from topic '%s', requeue=%t", logMessagePrefix, d.MessageId, topic, requeue)
			if err = r.channel.Nack(d.DeliveryTag, false, requeue); err != nil {
				log.Errorf("%s error nacking message '%s' from topic '%s', %s", logMessagePrefix, d.MessageId, topic, err)
			}
		} else {
			log.Debugf("%s acking message '%s' from topic '%s'", logMessagePrefix, d.MessageId, topic)
			if err = r.channel.Ack(d.DeliveryTag, false); err != nil {
				log.Errorf("%s error acking message '%s' from topic '%s', %s", logMessagePrefix, d.MessageId, topic, err)
			}
		}
	}
}

func (r *rabbitMQ) ensureExchangeDeclared(exchange string) error {
	if _, exists := r.declaredExchanges[exchange]; !exists {
		log.Debugf("%s declaring exchange '%s' of kind '%s'", logMessagePrefix, exchange, fanoutExchangeKind)
		err := r.channel.ExchangeDeclare(exchange, fanoutExchangeKind, true, false, false, false, nil)
		if err != nil {
			return err
		}

		r.declaredExchanges[exchange] = true
	}

	return nil
}
