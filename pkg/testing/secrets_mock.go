// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package testing

import "github.com/dapr/components-contrib/secretstores"

type FakeSecretStore struct {
}

func (c FakeSecretStore) GetSecret(req secretstores.GetSecretRequest) (secretstores.GetSecretResponse, error) {
	if req.Name == "good-key" {
		return secretstores.GetSecretResponse{
			Data: map[string]string{"good-key": "life is good"},
		}, nil
	}
	return secretstores.GetSecretResponse{Data: nil}, nil
}

func (c FakeSecretStore) Init(metadata secretstores.Metadata) error {
	return nil
}
