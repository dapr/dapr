// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package testing

import (
	"errors"

	"github.com/dapr/components-contrib/secretstores"
)

type FakeSecretStore struct{}

func (c FakeSecretStore) GetSecret(req secretstores.GetSecretRequest) (secretstores.GetSecretResponse, error) {
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

func (c FakeSecretStore) BulkGetSecret(req secretstores.BulkGetSecretRequest) (secretstores.BulkGetSecretResponse, error) {
	response := map[string]map[string]string{}
	response["good-key"] = map[string]string{"good-key": "life is good"}

	return secretstores.BulkGetSecretResponse{
		Data: response,
	}, nil
}

func (c FakeSecretStore) Init(metadata secretstores.Metadata) error {
	return nil
}

func (c FakeSecretStore) Close() error {
	return nil
}
