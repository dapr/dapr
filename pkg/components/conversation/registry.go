/*
Copyright 2024 The Dapr Authors
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

package conversation

import (
	"fmt"
	"strings"

	"github.com/dapr/components-contrib/conversation"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/kit/logger"
)

// Registry is an interface for a component that returns registered conversation implementations.
type Registry struct {
	Logger        logger.Logger
	conversations map[string]func(logger.Logger) conversation.Conversation
}

// DefaultRegistry is the singleton with the registry.
var DefaultRegistry *Registry = NewRegistry()

// NewRegistry is used to create conversation registry.
func NewRegistry() *Registry {
	return &Registry{
		Logger:        logger.NewLogger("dapr.conversation.registry"),
		conversations: make(map[string]func(logger.Logger) conversation.Conversation),
	}
}

// RegisterComponent adds a new conversation to the registry.
func (s *Registry) RegisterComponent(componentFactory func(logger.Logger) conversation.Conversation, names ...string) {
	for _, name := range names {
		s.conversations[createFullName(name)] = componentFactory
	}
}

func (s *Registry) Create(name, version, logName string) (conversation.Conversation, error) {
	if method, ok := s.getConversation(name, version, logName); ok {
		return method(), nil
	}
	return nil, fmt.Errorf("couldn't find conversation %s/%s", name, version)
}

func (s *Registry) getConversation(name, version, logName string) (func() conversation.Conversation, bool) {
	name = strings.ToLower(name)
	version = strings.ToLower(version)
	stateStoreFn, ok := s.conversations[name+"/"+version]

	if ok {
		return s.wrapFn(stateStoreFn, logName), true
	}
	if components.IsInitialVersion(version) {
		stateStoreFn, ok = s.conversations[name]
		if ok {
			return s.wrapFn(stateStoreFn, logName), true
		}
	}

	return nil, false
}

func (s *Registry) wrapFn(componentFactory func(logger.Logger) conversation.Conversation, logName string) func() conversation.Conversation {
	return func() conversation.Conversation {
		l := s.Logger
		if logName != "" && l != nil {
			l = l.WithFields(map[string]any{
				"component": logName,
			})
		}
		return componentFactory(l)
	}
}

func createFullName(name string) string {
	return strings.ToLower("conversation." + name)
}
