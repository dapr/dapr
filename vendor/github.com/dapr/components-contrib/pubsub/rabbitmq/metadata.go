package rabbitmq

import (
	"fmt"
	"strconv"

	"github.com/dapr/components-contrib/pubsub"
)

type metadata struct {
	consumerID       string
	host             string
	durable          bool
	deleteWhenUnused bool
	autoAck          bool
	requeueInFailure bool
	deliveryMode     uint8 // Transient (0 or 1) or Persistent (2)
}

// createMetadata creates a new instance from the pubsub metadata
func createMetadata(pubSubMetadata pubsub.Metadata) (*metadata, error) {
	result := metadata{deleteWhenUnused: true, autoAck: false}

	if val, found := pubSubMetadata.Properties[metadataHostKey]; found && val != "" {
		result.host = val
	} else {
		return &result, fmt.Errorf("%s missing RabbitMQ host", errorMessagePrefix)
	}

	if val, found := pubSubMetadata.Properties[metadataConsumerIDKey]; found && val != "" {
		result.consumerID = val
	} else {
		return &result, fmt.Errorf("%s missing RabbitMQ consumerID", errorMessagePrefix)
	}

	if val, found := pubSubMetadata.Properties[metadataDeliveryModeKey]; found && val != "" {
		if intVal, err := strconv.Atoi(val); err == nil {
			if intVal < 0 || intVal > 2 {
				return &result, fmt.Errorf("%s invalid RabbitMQ delivery mode, accepted values are between 0 and 2", errorMessagePrefix)
			}
			result.deliveryMode = uint8(intVal)
		}
	}

	if val, found := pubSubMetadata.Properties[metadataDurableKey]; found && val != "" {
		if boolVal, err := strconv.ParseBool(val); err == nil {
			result.durable = boolVal
		}
	}

	if val, found := pubSubMetadata.Properties[metadataDeleteWhenUnusedKey]; found && val != "" {
		if boolVal, err := strconv.ParseBool(val); err == nil {
			result.deleteWhenUnused = boolVal
		}
	}

	if val, found := pubSubMetadata.Properties[metadataAutoAckKey]; found && val != "" {
		if boolVal, err := strconv.ParseBool(val); err == nil {
			result.autoAck = boolVal
		}
	}

	if val, found := pubSubMetadata.Properties[metadataRequeueInFailureKey]; found && val != "" {
		if boolVal, err := strconv.ParseBool(val); err == nil {
			result.requeueInFailure = boolVal
		}
	}

	return &result, nil
}
