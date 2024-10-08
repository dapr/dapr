/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package secretstores

import (
	"context"

	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/dapr/pkg/components/pluggable"
	proto "github.com/dapr/dapr/pkg/proto/components/v1"
	"github.com/dapr/kit/logger"
)

// grpcSecretStore is a implementation of a secret store over a gRPC Protocol.
type grpcSecretStore struct {
	*pluggable.GRPCConnector[proto.SecretStoreClient]
	// features is the list of state store implemented features.
	features []secretstores.Feature
}

// Init initializes the grpc secret store passing out the metadata to the grpc component.
func (gss *grpcSecretStore) Init(ctx context.Context, metadata secretstores.Metadata) error {
	if err := gss.Dial(metadata.Name); err != nil {
		return err
	}

	protoMetadata := &proto.MetadataRequest{
		Properties: metadata.Properties,
	}

	_, err := gss.Client.Init(gss.Context, &proto.SecretStoreInitRequest{
		Metadata: protoMetadata,
	})
	if err != nil {
		return err
	}

	// TODO Static data could be retrieved in another way, a necessary discussion should start soon.
	// we need to call the method here because features could return an error and the features interface doesn't support errors
	featureResponse, err := gss.Client.Features(gss.Context, &proto.FeaturesRequest{})
	if err != nil {
		return err
	}

	gss.features = make([]secretstores.Feature, len(featureResponse.GetFeatures()))
	for idx, f := range featureResponse.GetFeatures() {
		gss.features[idx] = secretstores.Feature(f)
	}

	return nil
}

// Features lists all implemented features.
func (gss *grpcSecretStore) Features() []secretstores.Feature {
	return gss.features
}

// GetSecret retrieves a secret using a key and returns a map of decrypted string/string values.
func (gss *grpcSecretStore) GetSecret(ctx context.Context, req secretstores.GetSecretRequest) (secretstores.GetSecretResponse, error) {
	resp, err := gss.Client.Get(gss.Context, &proto.GetSecretRequest{
		Key:      req.Name,
		Metadata: req.Metadata,
	})
	if err != nil {
		return secretstores.GetSecretResponse{}, err
	}
	return secretstores.GetSecretResponse{
		Data: resp.GetData(),
	}, nil
}

// BulkGetSecret retrieves all secrets and returns a map of decrypted string/string values.
func (gss *grpcSecretStore) BulkGetSecret(ctx context.Context, req secretstores.BulkGetSecretRequest) (secretstores.BulkGetSecretResponse, error) {
	resp, err := gss.Client.BulkGet(gss.Context, &proto.BulkGetSecretRequest{
		Metadata: req.Metadata,
	})
	if err != nil {
		return secretstores.BulkGetSecretResponse{}, err
	}

	items := make(map[string]map[string]string, len(resp.GetData()))
	for k, v := range resp.GetData() {
		s := v.GetSecrets()
		items[k] = make(map[string]string, len(s))
		for k2, v2 := range s {
			items[k][k2] = v2
		}
	}
	return secretstores.BulkGetSecretResponse{
		Data: items,
	}, nil
}

// fromConnector creates a new GRPC pubsub using the given underlying connector.
func fromConnector(l logger.Logger, connector *pluggable.GRPCConnector[proto.SecretStoreClient]) *grpcSecretStore {
	return &grpcSecretStore{
		features:      make([]secretstores.Feature, 0),
		GRPCConnector: connector,
	}
}

// NewGRPCSecretStore creates a new grpc pubsub using the given socket factory.
func NewGRPCSecretStore(l logger.Logger, socket string) *grpcSecretStore {
	return fromConnector(l, pluggable.NewGRPCConnector(socket, proto.NewSecretStoreClient))
}

// newGRPCSecretStore creates a new grpc pubsub for the given pluggable component.
func newGRPCSecretStore(dialer pluggable.GRPCConnectionDialer) func(l logger.Logger) secretstores.SecretStore {
	return func(l logger.Logger) secretstores.SecretStore {
		return fromConnector(l, pluggable.NewGRPCConnectorWithDialer(dialer, proto.NewSecretStoreClient))
	}
}

func init() {
	//nolint:nosnakecase
	pluggable.AddServiceDiscoveryCallback(proto.SecretStore_ServiceDesc.ServiceName, func(name string, dialer pluggable.GRPCConnectionDialer) {
		DefaultRegistry.RegisterComponent(newGRPCSecretStore(dialer), name)
	})
}
