package v2alpha1_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
	"github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
)

func TestConversion(t *testing.T) {
	// Test converting to and from v1alpha1
	subscriptionV2 := v2alpha1.Subscription{
		Spec: v2alpha1.SubscriptionSpec{
			Pubsubname: "testPubSub",
			Topic:      "topicName",
			Metadata: map[string]string{
				"testName": "testValue",
			},
			Routes: v2alpha1.Routes{
				Default: "testPath",
			},
		},
	}

	var subscriptionV1 v1alpha1.Subscription
	err := subscriptionV2.ConvertTo(&subscriptionV1)
	require.NoError(t, err)

	var actual v2alpha1.Subscription
	err = actual.ConvertFrom(&subscriptionV1)
	require.NoError(t, err)

	assert.Equal(t, &subscriptionV2, &actual)
}
