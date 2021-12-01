/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

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
