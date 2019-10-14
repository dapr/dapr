// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package pubsub

import (
	"fmt"
	"sync"

	"github.com/dapr/components-contrib/pubsub"
)

// Registry is the interface for callers to get registered pub-sub components
type Registry interface {
	CreatePubSub(name string) (pubsub.PubSub, error)
}

type pubSubRegistry struct {
	messageBuses map[string]func() pubsub.PubSub
}

var instance *pubSubRegistry
var once sync.Once

// NewRegistry returns a new pub sub registry
func NewRegistry() Registry {
	once.Do(func() {
		instance = &pubSubRegistry{
			messageBuses: map[string]func() pubsub.PubSub{},
		}
	})
	return instance
}

// RegisterMessageBus registers a new message bus
func RegisterMessageBus(name string, factoryMethod func() pubsub.PubSub) {
	instance.messageBuses[createFullName(name)] = factoryMethod
}

func createFullName(name string) string {
	return fmt.Sprintf("pubsub.%s", name)
}

func (p *pubSubRegistry) CreatePubSub(name string) (pubsub.PubSub, error) {
	if method, ok := p.messageBuses[name]; ok {
		return method(), nil
	}
	return nil, fmt.Errorf("couldn't find message bus %s", name)
}
