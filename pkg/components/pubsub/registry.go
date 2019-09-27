package pubsub

import (
	"fmt"
	"sync"

	"github.com/actionscore/components-contrib/pubsub"
)

// PubSubRegistry is used to get registered message bus implementations
type PubSubRegistry interface {
	CreatePubSub(name string) (pubsub.PubSub, error)
}

type pubSubRegistry struct {
	messageBuses map[string]pubsub.PubSub
}

var instance *pubSubRegistry
var once sync.Once

// NewPubSubRegsitry returns a new pub sub registry
func NewPubSubRegsitry() PubSubRegistry {
	once.Do(func() {
		instance = &pubSubRegistry{
			messageBuses: map[string]pubsub.PubSub{},
		}
	})
	return instance
}

// RegisterMessageBus registers a new message bus
func RegisterMessageBus(name string, pubSub pubsub.PubSub) {
	instance.messageBuses[createFullName(name)] = pubSub
}

func createFullName(name string) string {
	return fmt.Sprintf("pubsub.%s", name)
}

func (p *pubSubRegistry) CreatePubSub(name string) (pubsub.PubSub, error) {
	if val, ok := p.messageBuses[name]; ok {
		return val, nil
	}

	return nil, fmt.Errorf("couldn't find message bus %s", name)
}
