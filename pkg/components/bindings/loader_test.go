package bindings

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOutputBindingRegistrations(t *testing.T) {
	registry := NewRegistry()
	Load()

	bindingsToCheck := []string{
		"aws.sqs",
		"aws.sns",
		"azure.eventhubs",
		"aws.dynamodb",
		"azure.cosmosdb",
		"gcp.bucket",
		"http",
		"kafka",
		"mqtt",
		"rabbitmq",
		"redis",
		"aws.s3",
		"azure.blobstorage",
		"azure.servicebusqueues",
		"gcp.pubsub",
		"azure.signalr"}

	for _, b := range bindingsToCheck {
		t.Run("Output binding: "+b, func(t *testing.T) {
			b, err := registry.CreateOutputBinding("bindings." + b)
			assert.Nil(t, err)
			assert.NotNil(t, b)
		})
	}
}

func TestInputBindingRegistrations(t *testing.T) {
	registry := NewRegistry()
	Load()

	bindingsToCheck := []string{
		"aws.sqs",
		"azure.eventhubs",
		"kafka",
		"mqtt",
		"rabbitmq",
		"azure.servicebusqueues",
		"gcp.pubsub",
		"kubernetes",
	}

	for _, b := range bindingsToCheck {
		t.Run("Input binding: "+b, func(t *testing.T) {
			b, err := registry.CreateInputBinding("bindings." + b)
			assert.Nil(t, err)
			assert.NotNil(t, b)
		})
	}
}
