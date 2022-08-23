package security

import (
	"bytes"
	"os"
	"strings"
	"sync"
	"time"
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

// Default delay on requests after a failed auth, in ms.
const DefaultDelayOnFailed = 2000

// APIToken manages authentication via a shared API token from an environment variable.
type APIToken struct {
	// How long to pause the next request after a failed auth.
	DelayOnFailed time.Duration

	token             []byte
	lastAttemptFailed bool
	mu                sync.Mutex
}

// Init the object, reading from the environment.
func (a *APIToken) Init() {
	a.token = []byte(os.Getenv(APITokenEnvVar))
	if a.DelayOnFailed == 0 {
		a.DelayOnFailed = time.Duration(DefaultDelayOnFailed) * time.Millisecond
	}
}

// InitWithToken inits the object, setting a specific token.
// This is mostly used for testing.
func (a *APIToken) InitWithToken(val string) {
	a.token = []byte(val)
	if a.DelayOnFailed == 0 {
		a.DelayOnFailed = time.Duration(DefaultDelayOnFailed) * time.Millisecond
	}
}

// HasAPIToken returns true if Dapr is configured with an API token from an environment variable.
func (a *APIToken) HasAPIToken() bool {
	return len(a.token) > 0
}

// CheckAPIToken returns true if the passed API token matches the one configured for Dapr through an environment variable.
// Note that if the previous auth attempt failed (from anyone), this adds a delay before responding, to slow down attackers.
func (a *APIToken) CheckAPIToken(token []byte) bool {
	a.mu.Lock()
	if a.lastAttemptFailed {
		// Slow down attackers if the previous attempt failed
		time.Sleep(a.DelayOnFailed)
	}
	res := bytes.Compare(a.token, token) == 0
	a.lastAttemptFailed = !res
	a.mu.Unlock()
	return res
}

// GetAppToken returns the value of the app api token from an environment variable.
func GetAppToken() string {
	return os.Getenv(AppAPITokenEnvVar)
}

// ExcludedRoute returns whether a given HTTP route should be excluded from a token check.
func ExcludedRoute(route string) bool {
	for _, r := range excludedRoutes {
		if strings.Contains(route, r) {
			return true
		}
	}
	return false
}
