package action

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"

	"cloud.google.com/go/storage"
	log "github.com/Sirupsen/logrus"
	uuid "github.com/satori/go.uuid"
	"google.golang.org/api/option"
)

type GCPStorage struct {
	Spec EventSourceSpec
}

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

func NewGCPStorage() *GCPStorage {
	return &GCPStorage{}
}

func (g *GCPStorage) Init(eventSourceSpec EventSourceSpec) error {
	g.Spec = eventSourceSpec
	return nil
}

func (g *GCPStorage) Read(metadata interface{}) (interface{}, error) {
	return nil, nil
}

func (g *GCPStorage) ReadAsync(metadata interface{}, callback func([]byte) error) error {
	return nil
}

func (g *GCPStorage) Write(data interface{}) error {
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	b, err := json.Marshal(g.Spec.ConnectionInfo)
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

	dataBytes, err := json.Marshal(data)
	if err != nil {
		return err
	}

	if _, err := wc.Write(dataBytes); err != nil {
		return err
	}

	err = wc.Close()
	if err != nil {
		return err
	}

	log.Info("Written data to GCP Storage successfully")

	return nil
}
