package pubsub

import (
	"io/ioutil"
	"os"
	"testing"

	subscriptionsapi "github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
	"github.com/dapr/dapr/pkg/logger"
	"github.com/ghodss/yaml"
	"github.com/stretchr/testify/assert"
)

var log = logger.NewLogger("dapr.test")

func TestFilterSubscriptions(t *testing.T) {
	subs := []Subscription{
		{
			Topic: "topic0",
			Route: "topic0",
		},
		{
			Topic: "topic1",
		},
		{
			Topic: "topic1",
			Route: "custom/topic1",
		},
	}

	subs = filterSubscriptions(subs, log)
	assert.Len(t, subs, 2)
	assert.Equal(t, "topic0", subs[0].Topic)
	assert.Equal(t, "topic1", subs[1].Topic)
	assert.Equal(t, "custom/topic1", subs[1].Route)
}

func testDeclarativeSubscription() subscriptionsapi.Subscription {
	return subscriptionsapi.Subscription{
		Spec: subscriptionsapi.SubscriptionSpec{
			Topic:      "topic1",
			Route:      "myroute",
			Pubsubname: "pubsub",
		},
	}
}

func writeSubscriptionToDisk(subscription subscriptionsapi.Subscription, filePath string) {
	b, _ := yaml.Marshal(subscription)
	ioutil.WriteFile(filePath, b, 0600)
}

func TestDeclarativeSubscriptions(t *testing.T) {
	t.Run("load single valid subscription", func(t *testing.T) {
		s := testDeclarativeSubscription()
		s.Scopes = []string{"scope1"}

		filePath := "sub.yaml"
		writeSubscriptionToDisk(s, filePath)
		defer os.RemoveAll(filePath)

		subs := DeclarativeSelfHosted(".", log)
		assert.Len(t, subs, 1)
		assert.Equal(t, "topic1", subs[0].Topic)
		assert.Equal(t, "myroute", subs[0].Route)
		assert.Equal(t, "pubsub", subs[0].PubsubName)
		assert.Equal(t, "scope1", subs[0].Scopes[0])
	})
}
