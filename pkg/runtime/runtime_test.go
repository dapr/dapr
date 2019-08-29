package runtime

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/actionscore/actions/pkg/channel"
	http_channel "github.com/actionscore/actions/pkg/channel/http"
	channelt "github.com/actionscore/actions/pkg/channel/testing"
	"github.com/actionscore/actions/pkg/components"
	"github.com/actionscore/actions/pkg/components/pubsub"
	cpubsub "github.com/actionscore/actions/pkg/components/pubsub"
	"github.com/actionscore/actions/pkg/modes"

	"github.com/actionscore/actions/pkg/config"
	"github.com/stretchr/testify/assert"
)

const (
	TestRuntimeConfigID = "consumer0"
)

func TestNewRuntime(t *testing.T) {
	// act
	r := NewActionsRuntime(&Config{}, &config.Configuration{})

	// assert
	assert.NotNil(t, r, "runtime must be initiated")
}

func TestInitPubSub(t *testing.T) {
	rt := NewTestActionsRuntime()

	initMockPubSubForRuntime := func(rt *ActionsRuntime) *cpubsub.MockPubSub {
		mockPubSub := new(cpubsub.MockPubSub)
		cpubsub.RegisterMessageBus("mockPubSub", mockPubSub)

		expectedMetadata := pubsub.Metadata{
			ConnectionInfo: getFakeConnectionInfo(),
			Properties:     map[string]string{"consumerID": TestRuntimeConfigID},
		}

		mockPubSub.On("Init", expectedMetadata).Return(nil)
		mockPubSub.On(
			"Subscribe",
			mock.AnythingOfType("pubsub.SubscribeRequest"),
			mock.AnythingOfType("func(*pubsub.NewMessage) error")).Return(nil)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		return mockPubSub
	}

	t.Run("subscribe 2 topics", func(t *testing.T) {
		mockPubSub := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 2 topics via http app channel
		fakeHttpResponse := &channel.InvokeResponse{
			Metadata: map[string]string{http_channel.HTTPStatusCode: "200"},
			Data:     []byte("[ \"topic0\", \"topic1\" ]"),
		}

		mockAppChannel.On(
			"InvokeMethod",
			&channel.InvokeRequest{
				Method:   "actions/subscribe",
				Metadata: map[string]string{http_channel.HTTPVerb: http_channel.Get},
			}).Return(fakeHttpResponse, nil)

		// act
		err := rt.initPubSub()

		// assert
		assert.Nil(t, err)
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 2)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("subscribe 0 topics unless user app provides topic list", func(t *testing.T) {
		mockPubSub := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeHttpResponse := &channel.InvokeResponse{
			Metadata: map[string]string{http_channel.HTTPStatusCode: "404"},
			Data:     nil,
		}

		mockAppChannel.On(
			"InvokeMethod",
			&channel.InvokeRequest{
				Method:   "actions/subscribe",
				Metadata: map[string]string{http_channel.HTTPVerb: http_channel.Get},
			}).Return(fakeHttpResponse, nil)

		// act
		err := rt.initPubSub()

		// assert
		assert.Nil(t, err)
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 0)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})
}

func TestOnNewPublishedMessage(t *testing.T) {
	testPubSubMessage := &pubsub.NewMessage{
		Topic: "topic1",
		Data:  []byte("Test Message"),
	}

	expectedRequest := &channel.InvokeRequest{
		Method:   testPubSubMessage.Topic,
		Payload:  testPubSubMessage.Data,
		Metadata: map[string]string{http_channel.HTTPVerb: http_channel.Post},
	}

	rt := NewTestActionsRuntime()

	t.Run("succeeded to publish message to user app", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeHttpResponse := &channel.InvokeResponse{
			Metadata: map[string]string{http_channel.HTTPStatusCode: "200"},
			Data:     []byte("OK"),
		}

		mockAppChannel.On("InvokeMethod", expectedRequest).Return(fakeHttpResponse, nil)

		// act
		err := rt.onNewPublishedMessage(testPubSubMessage)

		// assert
		assert.Nil(t, err)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("failed to publish message to user app", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeHttpResponse := &channel.InvokeResponse{
			Metadata: map[string]string{http_channel.HTTPStatusCode: "500"},
			Data:     []byte("Internal Error"),
		}

		expectedClientError := fmt.Errorf("Internal Error")

		mockAppChannel.On("InvokeMethod", expectedRequest).Return(fakeHttpResponse, expectedClientError)

		// act
		err := rt.onNewPublishedMessage(testPubSubMessage)

		// assert
		assert.Equal(t, fmt.Errorf("error from app: %s", expectedClientError), err)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})
}

func getFakeConnectionInfo() map[string]string {
	return map[string]string{
		"host":     "localhost",
		"password": "fakePassword",
	}
}

func NewTestActionsRuntime() *ActionsRuntime {
	testRuntimeConfig := NewRuntimeConfig(
		TestRuntimeConfigID,
		"10.10.10.12",
		"10.10.10.11",
		DefaultAllowedOrigins,
		"globalConfig",
		DefaultComponentsPath,
		string(HTTPProtocol),
		string(modes.StandaloneMode),
		DefaultActionsHTTPPort,
		DefaultActionsGRPCPort,
		1024,
		DefaultProfilePort)

	rt := NewActionsRuntime(testRuntimeConfig, &config.Configuration{})
	rt.components = []components.Component{
		{
			Metadata: components.ComponentMetadata{
				Name: "Components",
			},
			Spec: components.ComponentSpec{
				Type:           "pubsub.mockPubSub",
				ConnectionInfo: getFakeConnectionInfo(),
				Properties:     nil,
			},
		},
	}

	return rt
}
