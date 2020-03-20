package scopes

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAllowedTopics(t *testing.T) {
	t.Run("subscriptions: no scopes", func(t *testing.T) {
		topics := GetScopedTopics(SubscriptionScopes, "test", map[string]string{})
		assert.Len(t, topics, 0)
	})

	t.Run("publications: no scopes", func(t *testing.T) {
		topics := GetScopedTopics(PublishingScopes, "test", map[string]string{})
		assert.Len(t, topics, 0)
	})

	t.Run("subscriptions: allowed 1 topic", func(t *testing.T) {
		topics := GetScopedTopics(SubscriptionScopes, "test", map[string]string{SubscriptionScopes: "test=topic1"})
		assert.Len(t, topics, 1)
		assert.Equal(t, topics[0], "topic1")
	})

	t.Run("publications: allowed 1 topic", func(t *testing.T) {
		topics := GetScopedTopics(PublishingScopes, "test", map[string]string{PublishingScopes: "test=topic1"})
		assert.Len(t, topics, 1)
		assert.Equal(t, topics[0], "topic1")
	})

	t.Run("allowed 2 publication, 2 subscriptions", func(t *testing.T) {
		p := map[string]string{SubscriptionScopes: "test=topic1,topic2", PublishingScopes: "test=topic3,topic4"}
		subTopics := GetScopedTopics(SubscriptionScopes, "test", p)
		assert.Len(t, subTopics, 2)
		assert.Equal(t, subTopics[0], "topic1")
		assert.Equal(t, subTopics[1], "topic2")

		pubTopics := GetScopedTopics(PublishingScopes, "test", p)
		assert.Len(t, pubTopics, 2)
		assert.Equal(t, pubTopics[0], "topic3")
		assert.Equal(t, pubTopics[1], "topic4")
	})

	t.Run("publications: allowed all, different app-id", func(t *testing.T) {
		topics := GetScopedTopics(PublishingScopes, "test", map[string]string{PublishingScopes: "test1=topic1"})
		assert.Len(t, topics, 0)
	})

	t.Run("subscriptions: allowed all, different app-id", func(t *testing.T) {
		topics := GetScopedTopics(SubscriptionScopes, "test", map[string]string{SubscriptionScopes: "test1=topic1"})
		assert.Len(t, topics, 0)
	})

	t.Run("get 2 allowed topics", func(t *testing.T) {
		topics := GetAllowedTopics(map[string]string{AllowedTopics: "topic1,topic2"})
		assert.Len(t, topics, 2)
	})
}
