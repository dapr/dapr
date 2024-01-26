/*
Copyright 2023 The Dapr Authors
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

package store

import (
	"sync"

	"github.com/dapr/dapr/pkg/middleware"
	"github.com/dapr/kit/logger"
)

// Store provides a dynamic dictionary to loaded Component Middleware
// implementations.
type Store[T middleware.Middleware] struct {
	log  logger.Logger
	lock sync.RWMutex

	// loaded is the set of currently loaded middlewares, mapped by name to
	// middleware implementation.
	loaded map[string]Item[T]
}

// Metadata is the metadata associated with a middleware.
type Metadata struct {
	Name    string
	Type    string
	Version string
}

// Item is a single middleware in the store. It holds the middleware
// implementation and the metadata associated with it.
type Item[T middleware.Middleware] struct {
	Metadata
	Middleware T
}

func New[T middleware.Middleware](kind string) *Store[T] {
	return &Store[T]{
		log:    logger.NewLogger("dapr.middleware." + kind),
		loaded: make(map[string]Item[T]),
	}
}

// Add adds a middleware to the store.
func (s *Store[T]) Add(item Item[T]) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.log.Infof("Adding %s/%s %s middleware", item.Type, item.Version, item.Name)
	if len(item.Version) == 0 {
		item.Version = "v1"
	}
	s.loaded[item.Name] = item
}

// Get returns a middleware from the store.
func (s *Store[T]) Get(m Metadata) (T, bool) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	version := m.Version
	if len(version) == 0 {
		version = "v1"
	}
	l, ok := s.loaded[m.Name]
	if !ok || m.Type != l.Type || version != l.Version {
		return nil, false
	}
	return l.Middleware, ok
}

// Remove removes a middleware from the store.
func (s *Store[T]) Remove(name string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	m, ok := s.loaded[name]
	if ok {
		s.log.Infof("Removing %s/%s %s middleware", m.Type, m.Version, m.Name)
	}
	delete(s.loaded, name)
}
