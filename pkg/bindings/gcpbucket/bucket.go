package gcpbucket

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"

	"cloud.google.com/go/storage"
	log "github.com/Sirupsen/logrus"
	"github.com/actionscore/actions/pkg/components/bindings"
	uuid "github.com/satori/go.uuid"
	"google.golang.org/api/option"
)

// GCPStorage allows saving data to GCP bucket storage
type GCPStorage struct {
	Spec bindings.Metadata
}

// GCPMetadata represents a GCP bucket config
type GCPMetadata struct {
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
	g.Spec = metadata
	return nil
}

func (g *GCPStorage) Write(req *bindings.WriteRequest) error {
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	b, err := json.Marshal(g.Spec.Properties)
	if err != nil {
		return err
	}

	clientOptions := option.WithCredentialsJSON(b)

	var metadata GCPMetadata
	err = json.Unmarshal(b, &metadata)
	if err != nil {
		return err
	}

	ctx := context.Background()
	client, err := storage.NewClient(ctx, clientOptions)
	if err != nil {
		log.Fatal(err)
	}

	name := uuid.NewV4()
	wc := client.Bucket(metadata.Bucket).Object(name.String()).NewWriter(ctx)

	if _, err := wc.Write(req.Data); err != nil {
		return err
	}

	err = wc.Close()
	if err != nil {
		return err
	}

	return nil
}
