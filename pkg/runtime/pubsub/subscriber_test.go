package pubsub

import (
	"fmt"
	"github.com/dapr/components-contrib/pubsub"
	pubsub_middleware "github.com/dapr/dapr/pkg/middleware/pubsub"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/scopes"
	daprt "github.com/dapr/dapr/pkg/testing"
	jsoniter "github.com/json-iterator/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"testing"
)

const (
	TestRuntimeConfigID  = "consumer0"
	TestPubsubName       = "testpubsub"
	TestSecondPubsubName = "testpubsub2"
)

func NewTestSubscriberService(mode modes.DaprMode) SubscriberService {

	subscriberService := SubscriberService{
		BuildPipelineFunc: func(handlerSpecs []HandlerSpec) (pubsub_middleware.Pipeline, error) {
			return pubsub_middleware.Pipeline{}, nil
		},
		OperationAccessValidatorFunc: func(pubsubName string, topic string) bool {
			return true
		},
		Json:        jsoniter.ConfigFastest,
		TracingFunc: nil,
		GetDeclarativeSubscriptionsFunc: func() []Subscription {
			return []Subscription{}
		},
		GetSubscriptionsFunc: func() []Subscription {
			return []Subscription{}
		},
	}

	return subscriberService

}

func getFakeProperties() map[string]string {
	return map[string]string{
		"host":                    "localhost",
		"password":                "fakePassword",
		"consumerID":              TestRuntimeConfigID,
		scopes.SubscriptionScopes: fmt.Sprintf("%s=topic0,topic1", TestRuntimeConfigID),
		scopes.PublishingScopes:   fmt.Sprintf("%s=topic0,topic1", TestRuntimeConfigID),
	}
}

func initMockPubSubForRuntime() (*daprt.MockPubSub, *daprt.MockPubSub) {
	mockPubSub := new(daprt.MockPubSub)
	mockPubSub2 := new(daprt.MockPubSub)

	expectedMetadata := pubsub.Metadata{
		Properties: getFakeProperties(),
	}

	mockPubSub.On("Init", expectedMetadata).Return(nil)
	mockPubSub.On(
		"Subscribe",
		mock.AnythingOfType("pubsub.SubscribeRequest"),
		mock.AnythingOfType("pubsub.Handler")).Return(nil)

	mockPubSub2.On("Init", expectedMetadata).Return(nil)
	mockPubSub2.On(
		"Subscribe",
		mock.AnythingOfType("pubsub.SubscribeRequest"),
		mock.AnythingOfType("pubsub.Handler")).Return(nil)

	return mockPubSub, mockPubSub2
}

func TestInitPubSub(t *testing.T) {

	t.Run("get topic routes but app channel is nil", func(t *testing.T) {
		rts := NewTestSubscriberService(modes.StandaloneMode)
		routes, err := rts.getTopicRoutes()
		assert.Nil(t, err)
		assert.Equal(t, 0, len(routes))
	})
}
