package security

import (
	"os"
)

/* #nosec */
const (
	// APITokenEnvVar is the environment variable for the api token
	APITokenEnvVar = "DAPR_API_TOKEN"
	// APITokenHeader is header name for http/gRPC calls to hold the token
	APITokenHeader = "dapr-api-token"
)

// GetAPIToken returns the value of the api token from an environment variable
func GetAPIToken() string {
	return os.Getenv(APITokenEnvVar)
}
