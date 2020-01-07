# Secret Stores

Secret Stores provide a common way to interact with different secret stores, cloud/edge/commercial or open-source.

Currently supported secret stores are:

* Kubernetes
* Azure KeyVault
* AWS Secret manager
* GCP Cloud KMS

## Implementing a new Secret Store

A compliant secret store needs to implement the following interface:

```
type SecretStore interface {
	// Init authenticates with the actual secret store and performs other init operation
	Init(metadata Metadata) error
	// GetSecret retrieves a secret using a key and returns a map of decrypted string/string values
	GetSecret(req GetSecretRequest) (GetSecretResponse, error)
}
```
