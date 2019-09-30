package pubsub

import (
	"context"
	"encoding/json"
	"fmt"

	"cloud.google.com/go/pubsub"
	"github.com/actionscore/components-contrib/bindings"
	"google.golang.org/api/option"
)

const (
	id          = "id"
	publishTime = "publishTime"
	topic       = "topic"
)

// GCPPubSub is an input/output binding for GCP Pub Sub
type GCPPubSub struct {
	client   *pubsub.Client
	metadata *pubSubMetadata
}

type pubSubMetadata struct {
	Topic               string `json:"topic"`
	Subscription        string `json:"subscription"`
	Type                string `json:"type"`
	ProjectID           string `json:"project_id"`
	PrivateKeyID        string `json:"private_key_id"`
	PrivateKey          string `json:"private_key"`
	ClientEmail         string `json:"client_email"`
	ClientID            string `json:"client_id"`
	AuthURI             string `json:"auth_uri"`
	TokenURI            string `json:"token_uri"`
	AuthProviderCertURL string `json:"auth_provider_x509_cert_url"`
	ClientCertURL       string `json:"client_x509_cert_url"`
}

// NewGCPPubSub returns a new GCPPubSub instance
func NewGCPPubSub() *GCPPubSub {
	return &GCPPubSub{}
}

// Init parses metadata and creates a new Pub Sub client
func (g *GCPPubSub) Init(metadata bindings.Metadata) error {
	b, err := g.parseMetadata(metadata)
	if err != nil {
		return err
	}

	var pubsubMeta pubSubMetadata
	err = json.Unmarshal(b, &pubsubMeta)
	if err != nil {
		return err
	}
	clientOptions := option.WithCredentialsJSON(b)
	ctx := context.Background()
	pubsubClient, err := pubsub.NewClient(ctx, pubsubMeta.ProjectID, clientOptions)
	if err != nil {
		return fmt.Errorf("error creating pubsub client: %s", err)
	}

	g.client = pubsubClient
	g.metadata = &pubsubMeta
	return nil
}

func (g *GCPPubSub) parseMetadata(metadata bindings.Metadata) ([]byte, error) {
	b, err := json.Marshal(metadata.Properties)
	return b, err
}

func (g *GCPPubSub) Read(handler func(*bindings.ReadResponse) error) error {
	sub := g.client.Subscription(g.metadata.Subscription)
	err := sub.Receive(context.Background(), func(ctx context.Context, m *pubsub.Message) {
		err := handler(&bindings.ReadResponse{
			Data:     m.Data,
			Metadata: map[string]string{id: m.ID, publishTime: m.PublishTime.String()},
		})
		if err == nil {
			m.Ack()
		}
	})
	return err
}

func (g *GCPPubSub) Write(req *bindings.WriteRequest) error {
	topicName := g.metadata.Topic
	if val, ok := req.Metadata[topic]; ok && val != "" {
		topicName = val
	}

	t := g.client.Topic(topicName)
	ctx := context.Background()
	_, err := t.Publish(ctx, &pubsub.Message{
		Data: req.Data,
	}).Get(ctx)
	return err
}
