package eventhubs

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/gob"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"time"

	eventhub "github.com/Azure/azure-event-hubs-go"
	log "github.com/Sirupsen/logrus"
	"github.com/actionscore/actions/pkg/components/bindings"
)

// AzureEventHubs allows sending/receiving Azure Event Hubs events
type AzureEventHubs struct {
	hub  *eventhub.Hub
	meta AzureEventHubsMetadata
}

// AzureEventHubsMetadata is Azure Event Hubs connection metadata
type AzureEventHubsMetadata struct {
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
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	b, err := json.Marshal(metadata.ConnectionInfo)
	if err != nil {
		return err
	}

	var ehMetadata AzureEventHubsMetadata
	err = json.Unmarshal(b, &ehMetadata)
	if err != nil {
		return err
	}

	a.meta = ehMetadata
	hub, err := eventhub.NewHubFromConnectionString(a.meta.ConnectionString)
	if err != nil {
		return err
	}

	a.hub = hub
	return nil
}

// GetBytes turns an interface{} to a byte array representation
func GetBytes(key interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(key)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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

// Read reads from eventhubs in a non-blocking fashion
func (a *AzureEventHubs) Read(handler func(*bindings.ReadResponse) error) error {
	callback := func(c context.Context, event *eventhub.Event) error {
		if a.meta.MessageAge != "" && event.SystemProperties != nil && event.SystemProperties.EnqueuedTime != nil {
			enqTime := *event.SystemProperties.EnqueuedTime
			d, err := time.ParseDuration(a.meta.MessageAge)
			if err != nil {
				log.Errorf("error parsing duration: %s", err)
				return nil
			} else if time.Since(enqTime) > d {
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

	if a.meta.ConsumerGroup != "" {
		log.Infof("eventhubs: using consumer group %s", a.meta.ConsumerGroup)
		ops = append(ops, eventhub.ReceiveWithConsumerGroup(a.meta.ConsumerGroup))

	}

	for _, partitionID := range runtimeInfo.PartitionIDs {
		_, err := a.hub.Receive(ctx, partitionID, callback, ops...)
		if err != nil {
			return err
		}
	}

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, os.Kill)
	<-signalChan

	a.hub.Close(context.Background())

	return nil
}
