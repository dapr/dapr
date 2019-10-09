// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package servicebusqueues

import (
	"context"
	"encoding/json"
	"time"

	servicebus "github.com/Azure/azure-service-bus-go"
	"github.com/dapr/components-contrib/bindings"
)

const (
	corellationID = "corellationID"
	label         = "label"
	id            = "id"
)

// AzureServiceBusQueues is an input/outbput binding reading from and sending events to Azure Service Bus queues
type AzureServiceBusQueues struct {
	metadata *serviceBusQueuesMetadata
	client   *servicebus.Queue
}

type serviceBusQueuesMetadata struct {
	ConnectionString string `json:"connectionString"`
	QueueName        string `json:"queueName"`
}

// NewAzureServiceBusQueues returns a new AzureServiceBusQueues instance
func NewAzureServiceBusQueues() *AzureServiceBusQueues {
	return &AzureServiceBusQueues{}
}

// Init parses connection properties and creates a new Service Bus Queue client
func (a *AzureServiceBusQueues) Init(metadata bindings.Metadata) error {
	meta, err := a.parseMetadata(metadata)
	if err != nil {
		return err
	}
	a.metadata = meta

	ns, err := servicebus.NewNamespace(servicebus.NamespaceWithConnectionString(a.metadata.ConnectionString))
	if err != nil {
		return err
	}

	client, err := ns.NewQueue(a.metadata.QueueName)
	if err != nil {
		return err
	}
	a.client = client
	return nil
}

func (a *AzureServiceBusQueues) parseMetadata(metadata bindings.Metadata) (*serviceBusQueuesMetadata, error) {
	b, err := json.Marshal(metadata.Properties)
	if err != nil {
		return nil, err
	}

	var m serviceBusQueuesMetadata
	err = json.Unmarshal(b, &m)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (a *AzureServiceBusQueues) Write(req *bindings.WriteRequest) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	msg := servicebus.NewMessage(req.Data)
	if val, ok := req.Metadata[id]; ok && val != "" {
		msg.ID = val
	}
	if val, ok := req.Metadata[corellationID]; ok && val != "" {
		msg.CorrelationID = val
	}
	err := a.client.Send(ctx, msg)
	return err
}

func (a *AzureServiceBusQueues) Read(handler func(*bindings.ReadResponse) error) error {
	var sbHandler servicebus.HandlerFunc = func(ctx context.Context, msg *servicebus.Message) error {
		err := handler(&bindings.ReadResponse{
			Data:     msg.Data,
			Metadata: map[string]string{id: msg.ID, corellationID: msg.CorrelationID, label: msg.Label},
		})
		if err == nil {
			return msg.Complete(ctx)
		}
		return msg.Abandon(ctx)
	}

	if err := a.client.Receive(context.Background(), sbHandler); err != nil {
		return err
	}
	return nil
}
