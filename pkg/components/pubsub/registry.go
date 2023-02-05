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
	"fmt"
	"strings"

	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/kit/logger"
)

type Registry struct {
	Logger       logger.Logger
	messageBuses map[string]func(logger.Logger) pubsub.PubSub
}

// DefaultRegistry is the singleton with the registry.
var DefaultRegistry *Registry = NewRegistry()

// NewRegistry returns a new pub sub registry.
func NewRegistry() *Registry {
	return &Registry{
		messageBuses: map[string]func(logger.Logger) pubsub.PubSub{},
	}
}

// RegisterComponent adds a new message bus to the registry.
func (p *Registry) RegisterComponent(componentFactory func(logger.Logger) pubsub.PubSub, names ...string) {
	for _, name := range names {
		p.messageBuses[createFullName(name)] = componentFactory
	}
}

// Create instantiates a pub/sub based on `name`.
func (p *Registry) Create(name, version, logName string) (pubsub.PubSub, error) {
	if method, ok := p.getPubSub(name, version, logName); ok {
		return method(), nil
	}
	return nil, fmt.Errorf("couldn't find message bus %s/%s", name, version)
}

func (p *Registry) getPubSub(name, version, logName string) (func() pubsub.PubSub, bool) {
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)
	pubSubFn, ok := p.messageBuses[nameLower+"/"+versionLower]
	if ok {
		return p.wrapFn(pubSubFn, logName), true
	}
	if components.IsInitialVersion(versionLower) {
		pubSubFn, ok = p.messageBuses[nameLower]
		if ok {
			return p.wrapFn(pubSubFn, logName), true
		}
	}

	return nil, false
}

func (p *Registry) wrapFn(componentFactory func(logger.Logger) pubsub.PubSub, logName string) func() pubsub.PubSub {
	return func() pubsub.PubSub {
		l := p.Logger
		if logName != "" && l != nil {
			l = l.WithFields(map[string]any{
				"component": logName,
			})
		}
		return componentFactory(l)
	}
}

func createFullName(name string) string {
	return strings.ToLower("pubsub." + name)
}
