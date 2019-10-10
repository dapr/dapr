// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package secretstores

// GetSecretResponse describes the response object for a secret returned from a secret store
type GetSecretResponse struct {
	Data map[string]string `json:"data"`
}
