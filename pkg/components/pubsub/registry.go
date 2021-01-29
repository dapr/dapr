// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package pubsub

import (
	"fmt"

	"github.com/pkg/errors"

	"github.com/dapr/components-contrib/pubsub"
)

type (
	// PubSub is a pub/sub component definition.
	PubSub struct {
		Name          string
		FactoryMethod func() pubsub.PubSub
	}

	// Registry is the interface for callers to get registered pub-sub components
	Registry interface {
		Register(components ...PubSub)
		Create(name, version string) (pubsub.PubSub, error)
	}

	pubSubRegistry struct {
		messageBuses map[string]func() pubsub.PubSub
	}
)

// New creates a PubSub.
func New(name string, factoryMethod func() pubsub.PubSub) PubSub {
	return PubSub{
		Name:          name,
		FactoryMethod: factoryMethod,
	}
}

// NewRegistry returns a new pub sub registry
func NewRegistry() Registry {
	return &pubSubRegistry{
		messageBuses: map[string]func() pubsub.PubSub{},
	}
}

// Register registers one or more new message buses.
func (p *pubSubRegistry) Register(components ...PubSub) {
	for _, component := range components {
		p.messageBuses[createFullName(component.Name)] = component.FactoryMethod
	}
}

// Create instantiates a pub/sub based on `name`.
func (p *pubSubRegistry) Create(name, version string) (pubsub.PubSub, error) {
	if method, ok := p.getPubSub(name, version); ok {
		return method(), nil
	}
	return nil, errors.Errorf("couldn't find message bus %s/%s", name, version)
}

func (p *pubSubRegistry) getPubSub(name, version string) (func() pubsub.PubSub, bool) {
	pubSubFn, ok := p.messageBuses[name+"/"+version]
	if ok {
		return pubSubFn, true
	}
	if version == "" || version == "v0" || version == "v1" {
		pubSubFn, ok = p.messageBuses[name]
	}
	return pubSubFn, ok
}

func createFullName(name string) string {
	return fmt.Sprintf("pubsub.%s", name)
}
