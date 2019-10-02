package blobstorage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/google/uuid"

	log "github.com/Sirupsen/logrus"

	"github.com/Azure/azure-storage-blob-go/azblob"
	"github.com/dapr/components-contrib/bindings"
)

const (
	blobName = "blobName"
)

// AzureBlobStorage allows saving blobs to an Azure Blob Storage account
type AzureBlobStorage struct {
	metadata     *blobStorageMetadata
	containerURL azblob.ContainerURL
}

type blobStorageMetadata struct {
	StorageAccount   string `json:"storageAccount"`
	StorageAccessKey string `json:"storageAccessKey"`
	Container        string `json:"container"`
}

// NewAzureBlobStorage returns a new CosmosDB instance
func NewAzureBlobStorage() *AzureBlobStorage {
	return &AzureBlobStorage{}
}

// Init performs metadata parsing
func (a *AzureBlobStorage) Init(metadata bindings.Metadata) error {
	m, err := a.parseMetadata(metadata)
	if err != nil {
		return err
	}
	a.metadata = m
	credential, err := azblob.NewSharedKeyCredential(m.StorageAccount, m.StorageAccessKey)
	if err != nil {
		return fmt.Errorf("Invalid credentials with error: %s", err.Error())
	}
	p := azblob.NewPipeline(credential, azblob.PipelineOptions{})

	containerName := a.metadata.Container
	URL, _ := url.Parse(
		fmt.Sprintf("https://%s.blob.core.windows.net/%s", m.StorageAccount, containerName))
	containerURL := azblob.NewContainerURL(*URL, p)

	ctx := context.Background()
	_, err = containerURL.Create(ctx, azblob.Metadata{}, azblob.PublicAccessNone)
	// Don't return error, container might already exist
	log.Debugf("error creating container: %s", err)
	a.containerURL = containerURL
	return nil
}

func (a *AzureBlobStorage) parseMetadata(metadata bindings.Metadata) (*blobStorageMetadata, error) {
	connInfo := metadata.Properties
	b, err := json.Marshal(connInfo)
	if err != nil {
		return nil, err
	}

	var m blobStorageMetadata
	err = json.Unmarshal(b, &m)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (a *AzureBlobStorage) Write(req *bindings.WriteRequest) error {
	name := ""
	if val, ok := req.Metadata[blobName]; ok && val != "" {
		name = val
	} else {
		name = uuid.New().String()
	}
	blobURL := a.containerURL.NewBlockBlobURL(name)
	_, err := azblob.UploadBufferToBlockBlob(context.Background(), req.Data, blobURL, azblob.UploadToBlockBlobOptions{
		Parallelism: 16,
	})
	return err
}
