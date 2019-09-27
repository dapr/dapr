package gcpbucket

import (
	"context"
	"encoding/json"

	"cloud.google.com/go/storage"
	"github.com/actionscore/components-contrib/bindings"
	uuid "github.com/satori/go.uuid"
	"google.golang.org/api/option"
)

// GCPStorage allows saving data to GCP bucket storage
type GCPStorage struct {
	metadata gcpMetadata
	client   *storage.Client
}

type gcpMetadata struct {
	Bucket              string `json:"bucket"`
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

// NewGCPStorage returns a new GCP storage instance
func NewGCPStorage() *GCPStorage {
	return &GCPStorage{}
}

// Init performs connction parsing
func (g *GCPStorage) Init(metadata bindings.Metadata) error {
	b, err := g.parseMetadata(metadata)
	if err != nil {
		return err
	}

	var gm gcpMetadata
	err = json.Unmarshal(b, &gm)
	if err != nil {
		return err
	}
	clientOptions := option.WithCredentialsJSON(b)
	ctx := context.Background()
	client, err := storage.NewClient(ctx, clientOptions)
	if err != nil {
		return err
	}

	g.metadata = gm
	g.client = client
	return nil
}

func (g *GCPStorage) parseMetadata(metadata bindings.Metadata) ([]byte, error) {
	b, err := json.Marshal(metadata.Properties)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (g *GCPStorage) Write(req *bindings.WriteRequest) error {
	name := ""
	if val, ok := req.Metadata["name"]; ok && val != "" {
		name = val
	} else {
		name = uuid.NewV4().String()
	}
	h := g.client.Bucket(g.metadata.Bucket).Object(name).NewWriter(context.Background())
	defer h.Close()
	if _, err := h.Write(req.Data); err != nil {
		return err
	}
	return nil
}
