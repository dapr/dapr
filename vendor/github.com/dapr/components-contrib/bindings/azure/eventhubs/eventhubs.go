// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package eventhubs

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"syscall"
	"time"

	eventhub "github.com/Azure/azure-event-hubs-go"
	log "github.com/sirupsen/logrus"
	"github.com/dapr/components-contrib/bindings"
)

// AzureEventHubs allows sending/receiving Azure Event Hubs events
type AzureEventHubs struct {
	hub      *eventhub.Hub
	metadata *azureEventHubsMetadata
}

type azureEventHubsMetadata struct {
	ConnectionString string `json:"connectionString"`
	ConsumerGroup    string `json:"consumerGroup"`
	MessageAge       string `json:"messageAge"`
}

// NewAzureEventHubs returns a new Azure Event hubs instance
func NewAzureEventHubs() *AzureEventHubs {
	return &AzureEventHubs{}
}

// Init performs metadata init
func (a *AzureEventHubs) Init(metadata bindings.Metadata) error {
	m, err := a.parseMetadata(metadata)
	if err != nil {
		return err
	}
	a.metadata = m
	hub, err := eventhub.NewHubFromConnectionString(a.metadata.ConnectionString)
	if err != nil {
		return err
	}

	a.hub = hub
	return nil
}

func (a *AzureEventHubs) parseMetadata(metadata bindings.Metadata) (*azureEventHubsMetadata, error) {
	b, err := json.Marshal(metadata.Properties)
	if err != nil {
		return nil, err
	}

	var eventHubsMetadata azureEventHubsMetadata
	err = json.Unmarshal(b, &eventHubsMetadata)
	if err != nil {
		return nil, err
	}
	return &eventHubsMetadata, nil
}

// Write posts an event hubs message
func (a *AzureEventHubs) Write(req *bindings.WriteRequest) error {
	err := a.hub.Send(context.Background(), &eventhub.Event{
		Data: req.Data,
	})
	if err != nil {
		return err
	}

	return nil
}

// Read gets messages from eventhubs in a non-blocking fashion
func (a *AzureEventHubs) Read(handler func(*bindings.ReadResponse) error) error {
	callback := func(c context.Context, event *eventhub.Event) error {
		if a.metadata.MessageAge != "" && event.SystemProperties != nil && event.SystemProperties.EnqueuedTime != nil {
			enqTime := *event.SystemProperties.EnqueuedTime
			d, err := time.ParseDuration(a.metadata.MessageAge)
			if err != nil {
				log.Errorf("error parsing duration: %s", err)
				return nil
			} else if time.Now().UTC().Sub(enqTime) > d {
				return nil
			}
		}
		if event != nil {
			handler(&bindings.ReadResponse{
				Data: event.Data,
			})
		}

		return nil
	}

	ctx := context.Background()
	runtimeInfo, err := a.hub.GetRuntimeInformation(ctx)
	if err != nil {
		return err
	}

	ops := []eventhub.ReceiveOption{
		eventhub.ReceiveWithLatestOffset(),
	}

	if a.metadata.ConsumerGroup != "" {
		log.Infof("eventhubs: using consumer group %s", a.metadata.ConsumerGroup)
		ops = append(ops, eventhub.ReceiveWithConsumerGroup(a.metadata.ConsumerGroup))
	}

	for _, partitionID := range runtimeInfo.PartitionIDs {
		_, err := a.hub.Receive(ctx, partitionID, callback, ops...)
		if err != nil {
			return err
		}
	}

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)
	<-signalChan

	a.hub.Close(context.Background())
	return nil
}
