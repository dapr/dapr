// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package kafka

import (
	"context"
	"crypto/tls"
	"errors"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/Shopify/sarama"
	log "github.com/Sirupsen/logrus"
	"github.com/dapr/components-contrib/bindings"
)

// Kafka allows reading/writing to a Kafka consumer group
type Kafka struct {
	producer      sarama.SyncProducer
	topics        []string
	consumerGroup string
	brokers       []string
	publishTopic  string
	authRequired  bool
	saslUsername  string
	saslPassword  string
}

type kafkaMetadata struct {
	Brokers       []string `json:"brokers"`
	Topics        []string `json:"topics"`
	PublishTopic  string   `json:"publishTopic"`
	ConsumerGroup string   `json:"consumerGroup"`
	AuthRequired  bool     `json:"authRequired"`
	SaslUsername  string   `json:"saslUsername"`
	SaslPassword  string   `json:"saslPassword"`
}

type consumer struct {
	ready    chan bool
	callback func(*bindings.ReadResponse) error
}

func (consumer *consumer) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for message := range claim.Messages() {
		if consumer.callback != nil {
			err := consumer.callback(&bindings.ReadResponse{
				Data: message.Value,
			})
			if err == nil {
				session.MarkMessage(message, "")
			}
		}
	}
	return nil
}

func (consumer *consumer) Setup(sarama.ConsumerGroupSession) error {
	close(consumer.ready)
	return nil
}

// NewKafka returns a new kafka binding instance
func NewKafka() *Kafka {
	return &Kafka{}
}

// Init does metadata parsing and connection establishment
func (k *Kafka) Init(metadata bindings.Metadata) error {
	meta, err := k.getKafkaMetadata(metadata)
	if err != nil {
		return err
	}

	p, err := k.getSyncProducer(meta)
	if err != nil {
		return err
	}

	k.brokers = meta.Brokers
	k.producer = p
	k.topics = meta.Topics
	k.publishTopic = meta.PublishTopic
	k.consumerGroup = meta.ConsumerGroup
	k.authRequired = meta.AuthRequired

	//ignore SASL properties if authRequired is false
	if meta.AuthRequired {
		k.saslUsername = meta.SaslUsername
		k.saslPassword = meta.SaslPassword
	}
	return nil
}

func (k *Kafka) Write(req *bindings.WriteRequest) error {
	_, _, err := k.producer.SendMessage(&sarama.ProducerMessage{
		Topic: k.publishTopic,
		Value: sarama.ByteEncoder(req.Data),
	})
	if err != nil {
		return err
	}

	return nil
}

// GetKafkaMetadata returns new Kafka metadata
func (k *Kafka) getKafkaMetadata(metadata bindings.Metadata) (*kafkaMetadata, error) {
	meta := kafkaMetadata{}
	meta.ConsumerGroup = metadata.Properties["consumerGroup"]
	meta.PublishTopic = metadata.Properties["publishTopic"]

	if val, ok := metadata.Properties["brokers"]; ok && val != "" {
		meta.Brokers = strings.Split(val, ",")
	}
	if val, ok := metadata.Properties["topics"]; ok && val != "" {
		meta.Topics = strings.Split(val, ",")
	}

	val, ok := metadata.Properties["authRequired"]
	if !ok {
		return nil, errors.New("kafka error: missing 'authRequired' attribute")
	}
	if val == "" {
		return nil, errors.New("kafka error: 'authRequired' attribute was empty")
	}
	validAuthRequired, err := strconv.ParseBool(val)

	if err != nil {
		return nil, errors.New("kafka error: invalid value for 'authRequired' attribute")
	}
	meta.AuthRequired = validAuthRequired

	//ignore SASL properties if authRequired is false
	if meta.AuthRequired {
		if val, ok := metadata.Properties["saslUsername"]; ok && val != "" {
			meta.SaslUsername = val
		} else {
			return nil, errors.New("kafka error: missing SASL Username")
		}

		if val, ok := metadata.Properties["saslPassword"]; ok && val != "" {
			meta.SaslPassword = val
		} else {
			return nil, errors.New("kafka error: missing SASL Password")
		}
	}
	return &meta, nil
}

func (k *Kafka) getSyncProducer(meta *kafkaMetadata) (sarama.SyncProducer, error) {
	config := sarama.NewConfig()
	config.Producer.RequiredAcks = sarama.WaitForAll
	config.Producer.Retry.Max = 5
	config.Producer.Return.Successes = true
	config.Version = sarama.V1_0_0_0

	//ignore SASL properties if authRequired is false
	if meta.AuthRequired {
		updateAuthInfo(config, meta.SaslUsername, meta.SaslPassword)
	}

	producer, err := sarama.NewSyncProducer(meta.Brokers, config)
	if err != nil {
		return nil, err
	}
	return producer, nil
}

func (k *Kafka) Read(handler func(*bindings.ReadResponse) error) error {
	config := sarama.NewConfig()
	config.Version = sarama.V1_0_0_0
	//ignore SASL properties if authRequired is false
	if k.authRequired {
		updateAuthInfo(config, k.saslUsername, k.saslPassword)
	}
	c := consumer{
		callback: handler,
		ready:    make(chan bool),
	}

	client, err := sarama.NewConsumerGroup(k.brokers, k.consumerGroup, config)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())

	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			if err = client.Consume(ctx, k.topics, &c); err != nil {
				log.Errorf("error from c: %s", err)
			}
			// check if context was cancelled, signaling that the c should stop
			if ctx.Err() != nil {
				return
			}
			c.ready = make(chan bool)
		}
	}()

	<-c.ready

	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGINT, syscall.SIGTERM)
	<-sigterm
	cancel()
	wg.Wait()
	if err = client.Close(); err != nil {
		return err
	}
	return nil
}

func (consumer *consumer) Cleanup(sarama.ConsumerGroupSession) error {
	return nil
}
func updateAuthInfo(config *sarama.Config, saslUsername, saslPassword string) {
	config.Net.SASL.Enable = true
	config.Net.SASL.User = saslUsername
	config.Net.SASL.Password = saslPassword
	config.Net.SASL.Mechanism = sarama.SASLTypePlaintext

	config.Net.TLS.Enable = true
	config.Net.TLS.Config = &tls.Config{
		//InsecureSkipVerify: true,
		ClientAuth: 0,
	}
}
