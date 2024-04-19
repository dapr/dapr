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
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/middleware"
)

func TestStore(t *testing.T) {
	s := New[middleware.HTTP]("test")
	assert.Empty(t, s.loaded)

	item := Item[middleware.HTTP]{
		Metadata: Metadata{
			Name:    "test",
			Type:    "middleware",
			Version: "v2",
		},
		Middleware: func(http.Handler) http.Handler { return nil },
	}

	t.Run("can add middleware", func(t *testing.T) {
		s.Add(item)
		assert.Len(t, s.loaded, 1)
		assert.Equal(t, "test", s.loaded["test"].Name)
		assert.Equal(t, "middleware", s.loaded["test"].Type)
		assert.Equal(t, "v2", s.loaded["test"].Version)
	})

	t.Run("stored middlewares default to v1", func(t *testing.T) {
		item.Name = "test2"
		item.Version = ""
		s.Add(item)
		assert.Len(t, s.loaded, 2)
		assert.Equal(t, "v1", s.loaded["test2"].Version)
	})

	t.Run("can get middleware", func(t *testing.T) {
		m, ok := s.Get(Metadata{Name: "test", Type: "middleware", Version: "v2"})
		assert.True(t, ok)
		assert.NotNil(t, m)
	})

	t.Run("can get middleware defaults to v1", func(t *testing.T) {
		m, ok := s.Get(Metadata{Name: "test2", Type: "middleware"})
		assert.True(t, ok)
		assert.NotNil(t, m)
	})

	t.Run("returns false if middleware not found", func(t *testing.T) {
		_, ok := s.Get(Metadata{Name: "test3", Type: "middleware"})
		assert.False(t, ok)
	})

	t.Run("returns false if version mismatch", func(t *testing.T) {
		_, ok := s.Get(Metadata{Name: "test", Type: "middleware", Version: "v1"})
		assert.False(t, ok)
	})

	t.Run("returns false if defaulted version mismatch", func(t *testing.T) {
		m, ok := s.Get(Metadata{Name: "test", Type: "middleware", Version: ""})
		assert.False(t, ok)
		assert.Nil(t, m)
	})

	t.Run("returns false if type mismatch", func(t *testing.T) {
		_, ok := s.Get(Metadata{Name: "test", Type: "other", Version: "v2"})
		assert.False(t, ok)
	})

	t.Run("removing midllware that doesn't exist is a noop", func(t *testing.T) {
		s.Remove("test3")
		assert.Len(t, s.loaded, 2)
	})

	t.Run("can remove middleware", func(t *testing.T) {
		s.Remove("test")
		assert.Len(t, s.loaded, 1)
		m, ok := s.Get(Metadata{Name: "test", Type: "middleware", Version: "v2"})
		assert.False(t, ok)
		assert.Nil(t, m)

		s.Remove("test2")
		assert.Empty(t, s.loaded)
		m, ok = s.Get(Metadata{Name: "test2", Type: "middleware"})
		assert.False(t, ok)
		assert.Nil(t, m)
	})
}
