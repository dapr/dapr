package v1_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1 "github.com/dapr/dapr/pkg/apis/subscriptions/v1"
	"github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
)

func TestConversion(t *testing.T) {
	// Test converting to and from v1alpha1
	subscriptionV2 := v1.Subscription{
		Scopes: []string{"app1", "app2"},
		Spec: v1.SubscriptionSpec{
			Pubsubname: "testPubSub",
			Topic:      "topicName",
			Metadata: map[string]string{
				"testName": "testValue",
			},
			Routes: v1.Routes{
				Default: "testPath",
			},
			DeadLetterTopic: "testDeadLetterTopic",
			BulkSubscribe: v1.BulkSubscribe{
				Enabled:            true,
				MaxMessagesCount:   10,
				MaxAwaitDurationMs: 1000,
			},
		},
	}

	var subscriptionV1 v1alpha1.Subscription
	err := subscriptionV2.ConvertTo(&subscriptionV1)
	require.NoError(t, err)

	var actual v1.Subscription
	err = actual.ConvertFrom(&subscriptionV1)
	require.NoError(t, err)

	assert.Equal(t, &subscriptionV2, &actual)
}
