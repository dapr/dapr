package secretstores

// GetSecretResponse describes the response object for a secret returned from a secret store
type GetSecretResponse struct {
	Data map[string]string `json:"data"`
}
