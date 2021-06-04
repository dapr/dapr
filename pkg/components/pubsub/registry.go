// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package pubsub

import (
	"strings"

	"github.com/pkg/errors"

	"github.com/dapr/components-contrib/pubsub"

	"github.com/dapr/dapr/pkg/components"
)

type (
	// PubSub is a pub/sub component definition.
	PubSub struct {
		Name          string
		FactoryMethod func() pubsub.PubSub
	}

	// Registry is the interface for callers to get registered pub-sub components.
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

// NewRegistry returns a new pub sub registry.
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
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)
	pubSubFn, ok := p.messageBuses[nameLower+"/"+versionLower]
	if ok {
		return pubSubFn, true
	}
	if components.IsInitialVersion(versionLower) {
		pubSubFn, ok = p.messageBuses[nameLower]
	}
	return pubSubFn, ok
}

func createFullName(name string) string {
	return strings.ToLower("pubsub." + name)
}
