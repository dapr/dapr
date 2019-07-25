package runtime

import (
	"testing"

	"github.com/actionscore/actions/pkg/components"
	"github.com/actionscore/actions/pkg/components/pubsub"
	cpubsub "github.com/actionscore/actions/pkg/components/pubsub"
	"github.com/actionscore/actions/pkg/modes"

	"github.com/actionscore/actions/pkg/config"
	"github.com/stretchr/testify/assert"
)

const (
	RuntimeConfigID = "consumer0"
)

func getTestRuntimeConfig() *RuntimeConfig {
	return NewRuntimeConfig(
		RuntimeConfigID,
		"10.10.10.12",
		"10.10.10.11",
		DefaultAllowedOrigins,
		"globalConfig",
		DefaultComponentsPath,
		string(HTTPProtocol),
		string(modes.StandaloneMode),
		DefaultActionsHTTPPort,
		DefaultActionsGRPCPort,
		1024)
}
func TestNewRuntime(t *testing.T) {
	// act
	r := NewActionsRuntime(getTestRuntimeConfig(), &config.Configuration{})

	// assert
	assert.NotNil(t, r, "runtime must be initiated")
}

func TestInitPubSub(t *testing.T) {
	rt := NewActionsRuntime(getTestRuntimeConfig(), &config.Configuration{})

	fakeConnectionInfo := map[string]string{
		"host":     "localhost",
		"password": "fakePassword",
	}

	rt.components = []components.Component{
		{
			Metadata: components.ComponentMetadata{
				Name: "Components",
			},
			Spec: components.ComponentSpec{
				Type:           "pubsub.mockPubSub",
				ConnectionInfo: fakeConnectionInfo,
				Properties:     nil,
			},
		},
	}
	mockPubSub := new(cpubsub.MockPubSub)
	cpubsub.RegisterMessageBus("mockPubSub", mockPubSub)

	expectedMetadata := pubsub.Metadata{
		ConnectionInfo: fakeConnectionInfo,
		Properties:     map[string]string{"consumerID": RuntimeConfigID},
	}

	mockPubSub.On("Init", expectedMetadata).Return(nil)

	rt.initPubSub()
}

func TestGetSubscribedTopicsFromApp(t *testing.T) {
	//
}
