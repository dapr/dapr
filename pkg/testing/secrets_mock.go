// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package testing

import (
	"errors"

	"github.com/dapr/components-contrib/secretstores"
)

type FakeSecretStore struct {
}

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

func (c FakeSecretStore) BulkGetSecret(req secretstores.BulkGetSecretRequest) (secretstores.GetSecretResponse, error) {
	return secretstores.GetSecretResponse{}, nil
}

func (c FakeSecretStore) Init(metadata secretstores.Metadata) error {
	return nil
}
