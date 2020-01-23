// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package storagequeues

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Azure/azure-storage-queue-go/azqueue"
	"github.com/dapr/components-contrib/bindings"
	log "github.com/sirupsen/logrus"
)

type consumer struct {
	callback func(*bindings.ReadResponse) error
}

// QueueHelper enables injection for testnig
type QueueHelper interface {
	Init(accountName string, accountKey string, queueName string) error
	Write(data []byte) error
	Read(ctx context.Context, consumer *consumer) error
}

// AzureQueueHelper concrete impl of queue helper
type AzureQueueHelper struct {
	credential *azqueue.SharedKeyCredential
	queueURL   azqueue.QueueURL
	reqURI     string
}

// Init sets up this helper
func (d *AzureQueueHelper) Init(accountName string, accountKey string, queueName string) error {
	credential, err := azqueue.NewSharedKeyCredential(accountName, accountKey)
	if err != nil {
		return err
	}
	d.credential = credential
	u, _ := url.Parse(fmt.Sprintf(d.reqURI, accountName, queueName))
	d.queueURL = azqueue.NewQueueURL(*u, azqueue.NewPipeline(credential, azqueue.PipelineOptions{}))
	ctx := context.TODO()
	_, err = d.queueURL.Create(ctx, azqueue.Metadata{})
	if err != nil {
		return err
	}
	return nil
}

func (d *AzureQueueHelper) Write(data []byte) error {
	ctx := context.TODO()
	messagesURL := d.queueURL.NewMessagesURL()
	s := string(data)
	_, err := messagesURL.Enqueue(ctx, s, time.Second*0, time.Minute*10)
	return err
}

func (d *AzureQueueHelper) Read(ctx context.Context, consumer *consumer) error {
	messagesURL := d.queueURL.NewMessagesURL()
	res, err := messagesURL.Dequeue(ctx, 1, time.Second*30)
	if err != nil {
		return err
	}
	if res.NumMessages() == 0 {
		// Queue was empty so back off by 10 seconds before trying again
		time.Sleep(10 * time.Second)
		return nil
	}
	mt := res.Message(0).Text
	err = consumer.callback(&bindings.ReadResponse{
		Data:     []byte(mt),
		Metadata: map[string]string{},
	})
	if err != nil {
		return err
	}
	messageIDURL := messagesURL.NewMessageIDURL(res.Message(0).ID)
	pr := res.Message(0).PopReceipt
	_, err = messageIDURL.Delete(ctx, pr)
	if err != nil {
		return err
	}
	return nil
}

// NewAzureQueueHelper creates new helper
func NewAzureQueueHelper() QueueHelper {
	return &AzureQueueHelper{reqURI: "https://%s.queue.core.windows.net/%s"}
}

// AzureStorageQueues is an input/output binding reading from and sending events to Azure Storage queues
type AzureStorageQueues struct {
	metadata *storageQueuesMetadata
	helper   QueueHelper
}

type storageQueuesMetadata struct {
	AccountKey  string `json:"storageAccessKey"`
	QueueName   string `json:"queue"`
	AccountName string `json:"storageAccount"`
}

// NewAzureStorageQueues returns a new AzureStorageQueues instance
func NewAzureStorageQueues() *AzureStorageQueues {
	return &AzureStorageQueues{helper: NewAzureQueueHelper()}
}

// Init parses connection properties and creates a new Storage Queue client
func (a *AzureStorageQueues) Init(metadata bindings.Metadata) error {
	meta, err := a.parseMetadata(metadata)
	if err != nil {
		return err
	}
	a.metadata = meta

	err = a.helper.Init(a.metadata.AccountName, a.metadata.AccountKey, a.metadata.QueueName)
	if err != nil {
		return err
	}
	return nil
}

func (a *AzureStorageQueues) parseMetadata(metadata bindings.Metadata) (*storageQueuesMetadata, error) {
	b, err := json.Marshal(metadata.Properties)
	if err != nil {
		return nil, err
	}
	var m storageQueuesMetadata
	err = json.Unmarshal(b, &m)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (a *AzureStorageQueues) Write(req *bindings.WriteRequest) error {
	err := a.helper.Write(req.Data)
	if err != nil {
		return err
	}
	return nil
}

func (a *AzureStorageQueues) Read(handler func(*bindings.ReadResponse) error) error {
	c := consumer{
		callback: handler,
	}
	ctx, cancel := context.WithCancel(context.Background())
	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			err := a.helper.Read(ctx, &c)
			if err != nil {
				log.Errorf("error from c: %s", err)
			}
			if ctx.Err() != nil {
				return
			}
		}
	}()

	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGINT, syscall.SIGTERM)
	<-sigterm
	cancel()
	wg.Wait()

	return nil
}
