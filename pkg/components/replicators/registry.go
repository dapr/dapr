package replicators

import (
	"fmt"
	"strings"

	// Assuming the replicator interface and component are in components-contrib
	// This path is hypothetical and would point to the actual interface definition
	// in components-contrib once it exists. For now, we'll define a placeholder.
	// "github.com/dapr/components-contrib/state/replicator"
	"github.com/dapr/kit/logger"
)

// Placeholder for the actual StateReplicator interface from components-contrib
// In a real scenario, this would be imported.
type StateReplicator interface {
	Init(metadata interface{}, logger logger.Logger, getStateStore func(name string) (interface{}, error), getPubSub func(name string) (interface{}, error)) error
	Start(ctx interface{}) error // Using interface{} for context for now
	Stop(ctx interface{}) error
}


// Registry is an interface for a component that returns registered replicator implementations.
type Registry struct {
	Logger      logger.Logger
	replicators map[string]func(logger.Logger) StateReplicator
}

// DefaultRegistry is the singleton for the replicators registry.
var DefaultRegistry *Registry = NewRegistry()

// NewRegistry creates a new replicators registry.
func NewRegistry() *Registry {
	return &Registry{
		Logger:      logger.NewLogger("dapr.replicators.registry"),
		replicators: make(map[string]func(logger.Logger) StateReplicator),
	}
}

// RegisterComponent adds a new replicator to the registry.
func (r *Registry) RegisterComponent(componentFactory func(logger.Logger) StateReplicator, names ...string) {
	for _, name := range names {
		fullName := createFullName(name)
		r.replicators[fullName] = componentFactory
		r.Logger.Debugf("Registered replicator: %s", fullName)
	}
}

// Create instantiates a replicator based on its name.
func (r *Registry) Create(name, version, logName string) (StateReplicator, error) {
	fullName := createFullName(name)
	if factory, ok := r.replicators[fullName]; ok {
		l := r.Logger
		if logName != "" && l != nil {
			l = l.WithFields(map[string]any{"component": logName})
		}
		return factory(l), nil
	}
	return nil, fmt.Errorf("couldn't find replicator component named '%s'", name)
}

func createFullName(name string) string {
	return strings.ToLower("replicator." + name)
}
