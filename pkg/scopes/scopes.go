package scopes

import (
	"strings"
)

const (
	SubscriptionScopes = "subscriptionScopes"
	PublishingScopes   = "publishingScopes"
	AllowedTopics      = "allowedTopics"
	ProtectedTopics    = "protectedTopics"
	appsSeparator      = ";"
	appSeparator       = "="
	topicSeparator     = ","
)

func findParamInMetadata(param string, metadata map[string]string) (string, bool) {
	param = strings.ToLower(param)

	for name, val := range metadata {
		name = strings.ToLower(name)

		if param == name {
			return val, true
		}
	}

	return "", false
}

// GetScopedTopics returns a list of scoped topics for a given application from a Pub/Sub
// Component properties.
func GetScopedTopics(scope, appID string, metadata map[string]string) []string {
	var (
		existM = map[string]struct{}{}
		topics = []string{}
	)

	if val, ok := findParamInMetadata(scope, metadata); ok && val != "" {
		val = strings.ReplaceAll(val, " ", "")
		apps := strings.Split(val, appsSeparator)
		for _, a := range apps {
			appTopics := strings.Split(a, appSeparator)
			if len(appTopics) < 2 {
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

func getParamTopics(param string, metadata map[string]string) []string {
	var (
		existM = map[string]struct{}{}
		topics = []string{}
	)

	if val, ok := findParamInMetadata(param, metadata); ok && val != "" {
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

// GetAllowedTopics return the all topics list of params allowedTopics.
func GetAllowedTopics(metadata map[string]string) []string {
	return getParamTopics(AllowedTopics, metadata)
}

// GetProtectedTopics returns all topics of param protectedTopics.
func GetProtectedTopics(metadata map[string]string) []string {
	return getParamTopics(ProtectedTopics, metadata)
}
