/*
Copyright 2024 The Dapr Authors
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

package secretstore

import (
	"context"
	"io"
	"testing"

	"github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/secretstores"

	compv1pb "github.com/dapr/dapr/pkg/proto/components/v1"
)

type component struct {
	impl secretstores.SecretStore
}

func newComponent(t *testing.T, opts options) *component {
	return &component{
		impl: opts.secretStore,
	}
}

func (c *component) Init(ctx context.Context, request *compv1pb.SecretStoreInitRequest) (*compv1pb.SecretStoreInitResponse, error) {
	return new(compv1pb.SecretStoreInitResponse), c.impl.Init(ctx, secretstores.Metadata{
		Base: metadata.Base{
			Name:       "secretstores.local.file",
			Properties: request.GetMetadata().GetProperties(),
		},
	})
}

func (c *component) Features(ctx context.Context, request *compv1pb.FeaturesRequest) (*compv1pb.FeaturesResponse, error) {
	implF := c.impl.Features()
	features := make([]string, len(implF))
	for i, f := range implF {
		features[i] = string(f)
	}
	return &compv1pb.FeaturesResponse{
		Features: features,
	}, nil
}

func (c *component) Get(ctx context.Context, request *compv1pb.GetSecretRequest) (*compv1pb.GetSecretResponse, error) {
	resp, err := c.impl.GetSecret(ctx, secretstores.GetSecretRequest{
		Name:     request.GetKey(),
		Metadata: request.GetMetadata(),
	})
	if err != nil {
		return nil, err
	}

	return &compv1pb.GetSecretResponse{
		Data: resp.Data,
	}, nil
}

func (c *component) BulkGet(ctx context.Context, request *compv1pb.BulkGetSecretRequest) (*compv1pb.BulkGetSecretResponse, error) {
	resp, err := c.impl.BulkGetSecret(ctx, secretstores.BulkGetSecretRequest{
		Metadata: request.GetMetadata(),
	})
	if err != nil {
		return nil, err
	}
	data := make(map[string]*compv1pb.SecretResponse, len(resp.Data))
	for k, v := range resp.Data {
		data[k] = &compv1pb.SecretResponse{
			Secrets: v,
		}
	}
	return &compv1pb.BulkGetSecretResponse{
		Data: data,
	}, nil
}

func (c *component) Ping(ctx context.Context, request *compv1pb.PingRequest) (*compv1pb.PingResponse, error) {
	return new(compv1pb.PingResponse), nil
}

func (c *component) Close() error {
	return c.impl.(io.Closer).Close()
}
