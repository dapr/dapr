package scopes

import (
	"strings"
)

const (
	SubscriptionScopes = "subscriptionScopes"
	PublishingScopes   = "publishingScopes"
	AllowedTopics      = "allowedTopics"
	appsSeparator      = ";"
	appSeparator       = "="
	topicSeparator     = ","
)

// GetScopedTopics returns a list of scoped topics for a given application from a Pub/Sub
// Component properties.
func GetScopedTopics(scope, appID string, metadata map[string]string) []string {
	var (
		existM = map[string]struct{}{}
		topics = []string{}
	)

	if val, ok := metadata[scope]; ok && val != "" {
		val = strings.ReplaceAll(val, " ", "")
		apps := strings.Split(val, appsSeparator)
		for _, a := range apps {
			appTopics := strings.Split(a, appSeparator)
			if len(appTopics) == 0 {
				continue
			}

			app := appTopics[0]
			if app != appID {
				continue
			}

			tempTopics := strings.Split(appTopics[1], topicSeparator)
			for _, tempTopic := range tempTopics {
				if _, ok = existM[tempTopic]; !ok {
					existM[tempTopic] = struct{}{}
					topics = append(topics, tempTopic)
				}
			}
		}
	}
	return topics
}

// GetAllowedTopics return the all topics list of params allowedTopics.
func GetAllowedTopics(metadata map[string]string) []string {
	var (
		existM = map[string]struct{}{}
		topics = []string{}
	)

	if val, ok := metadata[AllowedTopics]; ok && val != "" {
		val = strings.ReplaceAll(val, " ", "")
		tempTopics := strings.Split(val, topicSeparator)
		for _, tempTopic := range tempTopics {
			if _, ok = existM[tempTopic]; !ok {
				existM[tempTopic] = struct{}{}
				topics = append(topics, tempTopic)
			}
		}
	}
	return topics
}
