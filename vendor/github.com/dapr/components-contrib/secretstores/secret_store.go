// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package secretstores

// SecretStore is the interface for a component that handles secrets management
type SecretStore interface {
	// Init authenticates with the actual secret store and performs other init operation
	Init(metadata Metadata) error
	// GetSecret retrieves a secret using a key and returns a map of decrypted string/string values
	GetSecret(req GetSecretRequest) (GetSecretResponse, error)
}
