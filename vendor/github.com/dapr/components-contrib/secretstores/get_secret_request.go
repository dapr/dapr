package secretstores

// GetSecretRequest describes a get secret request from a secret store
type GetSecretRequest struct {
	Name     string            `json:"name"`
	Metadata map[string]string `json:"metadata"`
}
