package replicators

import (
	"testing"

	"github.com/dapr/kit/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockReplicator is a mock implementation for testing.
type MockReplicator struct {
	logger      logger.Logger
	InitCalled  bool
	StartCalled bool
	StopCalled  bool
	Metadata    map[string]string
}

func (m *MockReplicator) Init(metadata interface{}, logger logger.Logger, getStateStore func(name string) (interface{}, error), getPubSub func(name string) (interface{}, error)) error {
	m.logger = logger
	m.InitCalled = true
	if metaMap, ok := metadata.(map[string]string); ok {
		m.Metadata = metaMap
	}
	return nil
}

func (m *MockReplicator) Start(ctx interface{}) error {
	m.StartCalled = true
	return nil
}

func (m *MockReplicator) Stop(ctx interface{}) error {
	m.StopCalled = true
	return nil
}

func NewMockReplicatorFactory(l logger.Logger) StateReplicator {
	return &MockReplicator{logger: l}
}

func TestRegistry(t *testing.T) {
	registry := NewRegistry()

	t.Run("register and create replicator", func(t *testing.T) {
		registry.RegisterComponent(NewMockReplicatorFactory, "mockreplicator")

		comp, err := registry.Create("mockreplicator", "v1", "test-log")
		require.NoError(t, err)
		require.NotNil(t, comp)

		mockComp, ok := comp.(*MockReplicator)
		require.True(t, ok)
		assert.NotNil(t, mockComp.logger)
	})

	t.Run("create unregistered replicator", func(t *testing.T) {
		_, err := registry.Create("nonexistent", "v1", "test-log")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "couldn't find replicator component named 'nonexistent'")
	})

	t.Run("register multiple names", func(t *testing.T) {
		registry.RegisterComponent(NewMockReplicatorFactory, "alias1", "alias2")

		comp1, err1 := registry.Create("alias1", "v1", "test-log")
		require.NoError(t, err1)
		assert.NotNil(t, comp1)

		comp2, err2 := registry.Create("alias2", "v1", "test-log")
		require.NoError(t, err2)
		assert.NotNil(t, comp2)
	})

	t.Run("full name creation", func(t *testing.T) {
		registry.RegisterComponent(NewMockReplicatorFactory, "testcomponent")
		// createFullName prepends "replicator."
		// Create() expects the short name.
		_, err := registry.Create("replicator.testcomponent", "v1", "test-log")
		require.Error(t, err, "Create should expect the short name, createFullName is an internal detail for storage")

		comp, err := registry.Create("testcomponent", "v1", "test-log")
		require.NoError(t, err)
		assert.NotNil(t, comp)
	})
}
