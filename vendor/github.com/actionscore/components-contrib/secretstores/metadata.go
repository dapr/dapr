package secretstores

// DefaultSecretRefKeyName is the default key if secretKeyRef.key is not given
const DefaultSecretRefKeyName = "_value"

// Metadata contains a secretstore specific set of metadata properties
type Metadata struct {
	Properties map[string]string `json:"properties,omitempty"`
}
