package kafka

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"syscall"

	"github.com/Shopify/sarama"
	"github.com/actionscore/actions/pkg/components/bindings"
)

// Kafka allows reading/writing to a Kafka consumer group
type Kafka struct {
	producer      sarama.SyncProducer
	topics        []string
	consumerGroup string
	brokers       []string
	publishTopic  string
}

// Metadata is the config object for Kafka
type Metadata struct {
	Brokers       []string `json:"brokers"`
	Topics        []string `json:"topics"`
	PublishTopic  string   `json:"publishTopic"`
	ConsumerGroup string   `json:"consumerGroup"`
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

// NewKafka returns a new kafka instance
func NewKafka() *Kafka {
	return &Kafka{}
}

// Init does metadata parsing and connection establishment
func (k *Kafka) Init(metadata bindings.Metadata) error {
	meta, err := k.GetKafkaMetadata(metadata)
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
func (k *Kafka) GetKafkaMetadata(metadata bindings.Metadata) (*Metadata, error) {
	b, err := json.Marshal(metadata.Properties)
	if err != nil {
		return nil, err
	}

	var meta Metadata
	err = json.Unmarshal(b, &meta)
	if err != nil {
		return nil, err
	}

	return &meta, nil
}

func (k *Kafka) getSyncProducer(meta *Metadata) (sarama.SyncProducer, error) {
	config := sarama.NewConfig()
	config.Producer.RequiredAcks = sarama.WaitForLocal
	config.Producer.Retry.Max = 10
	config.Producer.Return.Successes = false

	producer, err := sarama.NewSyncProducer(meta.Brokers, config)
	if err != nil {
		return nil, err
	}

	return producer, nil
}

func (k *Kafka) Read(handler func(*bindings.ReadResponse) error) error {
	config := sarama.NewConfig()
	config.Version = sarama.V1_0_0_0

	consumer := consumer{
		callback: handler,
	}

	ctx := context.Background()
	client, err := sarama.NewConsumerGroup(k.brokers, k.consumerGroup, config)
	if err != nil {
		return err
	}

	go func() {
		for {
			consumer.ready = make(chan bool, 0)
			client.Consume(ctx, k.topics, &consumer)
		}
	}()

	<-consumer.ready

	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGINT, syscall.SIGTERM)
	<-sigterm

	err = client.Close()
	if err != nil {
		return err
	}

	return nil
}

func (consumer *consumer) Cleanup(sarama.ConsumerGroupSession) error {
	return nil
}
