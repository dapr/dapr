//go:build unit
// +build unit

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

package mock

import (
	"context"

	"github.com/dapr/components-contrib/secretstores"
)

type SecretStore struct {
	secretstores.SecretStore
	CloseErr error
}

func (s *SecretStore) GetSecret(ctx context.Context, req secretstores.GetSecretRequest) (secretstores.GetSecretResponse, error) {
	return secretstores.GetSecretResponse{
		Data: map[string]string{
			"key1":   "value1",
			"_value": "_value_data",
			"name1":  "value1",
		},
	}, nil
}

func (s *SecretStore) Init(ctx context.Context, metadata secretstores.Metadata) error {
	return nil
}

func (s *SecretStore) Close() error {
	return s.CloseErr
}
