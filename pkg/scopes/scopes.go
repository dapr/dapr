package scopes

import "strings"

const (
	SubscriptionScopes = "subscriptionScopes"
	PublishingScopes   = "publishingScopes"
	appsSeperator      = ";"
	appSeperator       = "="
	topicSeperator     = ","
)

// GetScopedTopics returns a list of scoped topics for a given application from a Pub/Sub
// Component properties
func GetScopedTopics(scope, appID string, metadata map[string]string) []string {
	topics := []string{}

	if val, ok := metadata[scope]; ok && val != "" {
		apps := strings.Split(val, appsSeperator)
		for _, a := range apps {
			appTopics := strings.Split(a, appSeperator)
			if len(appTopics) > 1 {
				app := appTopics[0]
				if app != appID {
					continue
				}

				topics = strings.Split(appTopics[1], topicSeperator)
				break
			} else {
				break
			}
		}
	}
	return topics
}
