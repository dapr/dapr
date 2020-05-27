package security

import (
	"os"
)

const (
	APITokenEnvVar = "DAPR_API_TOKEN"
	APITokenHeader = "dapr-api-token"
)

func GetAPIToken() string {
	return os.Getenv(APITokenEnvVar)
}
