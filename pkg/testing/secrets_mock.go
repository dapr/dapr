/*
Copyright 2021 The Dapr Authors
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

package testing

import (
	"context"
	"errors"

	"github.com/dapr/components-contrib/secretstores"
)

type FakeSecretStore struct{}

func (c FakeSecretStore) GetSecret(ctx context.Context, req secretstores.GetSecretRequest) (secretstores.GetSecretResponse, error) {
	if req.Name == "good-key" {
		return secretstores.GetSecretResponse{
			Data: map[string]string{"good-key": "life is good"},
		}, nil
	}

	if req.Name == "error-key" {
		return secretstores.GetSecretResponse{}, errors.New("error occurs with error-key")
	}

	return secretstores.GetSecretResponse{}, nil
}

func (c FakeSecretStore) BulkGetSecret(ctx context.Context, req secretstores.BulkGetSecretRequest) (secretstores.BulkGetSecretResponse, error) {
	response := map[string]map[string]string{}
	response["good-key"] = map[string]string{"good-key": "life is good"}

	return secretstores.BulkGetSecretResponse{
		Data: response,
	}, nil
}

func (c FakeSecretStore) Init(ctx context.Context, metadata secretstores.Metadata) error {
	return nil
}

func (c FakeSecretStore) Close() error {
	return nil
}

func (c FakeSecretStore) Features() []secretstores.Feature {
	return []secretstores.Feature{secretstores.FeatureMultipleKeyValuesPerSecret}
}

type FailingSecretStore struct {
	Failure Failure
}

func (c FailingSecretStore) GetSecret(ctx context.Context, req secretstores.GetSecretRequest) (secretstores.GetSecretResponse, error) {
	err := c.Failure.PerformFailure(req.Name)
	if err != nil {
		return secretstores.GetSecretResponse{}, err
	}

	return secretstores.GetSecretResponse{
		Data: map[string]string{req.Name: "secret"},
	}, nil
}

func (c FailingSecretStore) BulkGetSecret(ctx context.Context, req secretstores.BulkGetSecretRequest) (secretstores.BulkGetSecretResponse, error) {
	key := req.Metadata["key"]
	err := c.Failure.PerformFailure(key)
	if err != nil {
		return secretstores.BulkGetSecretResponse{}, err
	}

	return secretstores.BulkGetSecretResponse{
		Data: map[string]map[string]string{},
	}, nil
}

func (c FailingSecretStore) Init(ctx context.Context, metadata secretstores.Metadata) error {
	return nil
}

func (c FailingSecretStore) Close() error {
	return nil
}

func (c FailingSecretStore) Features() []secretstores.Feature {
	return []secretstores.Feature{}
}
