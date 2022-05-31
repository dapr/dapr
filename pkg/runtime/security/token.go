package security

import (
	"os"
	"strings"
)

/* #nosec. */
const (
	// APITokenEnvVar is the environment variable for the api token.
	APITokenEnvVar    = "DAPR_API_TOKEN"
	AppAPITokenEnvVar = "APP_API_TOKEN"
	// APITokenHeader is header name for http/gRPC calls to hold the token.
	APITokenHeader = "dapr-api-token"
)

var excludedRoutes = []string{"/healthz"}

// GetAPIToken returns the value of the api token from an environment variable.
func GetAPIToken() string {
	return os.Getenv(APITokenEnvVar)
}

// GetAppToken returns the value of the app api token from an environment variable.
func GetAppToken() string {
	return os.Getenv(AppAPITokenEnvVar)
}

// ExcludedRoute returns whether a given route should be excluded from a token check.
func ExcludedRoute(route string) bool {
	for _, r := range excludedRoutes {
		if strings.Contains(route, r) {
			return true
		}
	}
	return false
}
