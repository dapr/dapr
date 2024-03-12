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

package http

import (
	nethttp "net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/config"
)

func TestHTTP(t *testing.T) {
	t.Run("if chain already built, should use chain", func(t *testing.T) {
		middle1 := newTestMiddle("test")
		middle2 := newTestMiddle("test2")
		h := New()
		h.Add(Spec{
			Component:      middle1.comp,
			Implementation: middle1.item.Middleware,
		})
		h.Add(Spec{
			Component:      middle2.comp,
			Implementation: middle2.item.Middleware,
		})

		var invoked int
		root := nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { invoked++ })
		handler := h.BuildPipelineFromSpec("test", &config.PipelineSpec{Handlers: []config.HandlerSpec{
			{Name: "test", Type: "middleware.http.fakemw", Version: "v1"},
			{Name: "test2", Type: "middleware.http.fakemw", Version: ""},
		}})(root)

		assert.Equal(t, 0, invoked)
		assert.Equal(t, int32(0), middle1.invoked.Load())
		assert.Equal(t, int32(0), middle2.invoked.Load())
		handler.ServeHTTP(nil, nil)
		assert.Equal(t, 1, invoked)
		assert.Equal(t, int32(1), middle1.invoked.Load())
		assert.Equal(t, int32(1), middle2.invoked.Load())
	})

	t.Run("if chain is added after, should use chain", func(t *testing.T) {
		middle1 := newTestMiddle("test")
		middle2 := newTestMiddle("test2")
		h := New()

		var invoked int
		root := nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { invoked++ })
		handler := h.BuildPipelineFromSpec("test", &config.PipelineSpec{Handlers: []config.HandlerSpec{
			{Name: "test", Type: "middleware.http.fakemw", Version: "v1"},
			{Name: "test2", Type: "middleware.http.fakemw", Version: ""},
		}})(root)

		assert.Equal(t, 0, invoked)
		assert.Equal(t, int32(0), middle1.invoked.Load())
		assert.Equal(t, int32(0), middle2.invoked.Load())
		handler.ServeHTTP(nil, nil)
		assert.Equal(t, 1, invoked)
		assert.Equal(t, int32(0), middle1.invoked.Load())
		assert.Equal(t, int32(0), middle2.invoked.Load())

		h.Add(Spec{
			Component:      middle1.comp,
			Implementation: middle1.item.Middleware,
		})
		h.Add(Spec{
			Component:      middle2.comp,
			Implementation: middle2.item.Middleware,
		})
		handler.ServeHTTP(nil, nil)
		assert.Equal(t, 2, invoked)
		assert.Equal(t, int32(1), middle1.invoked.Load())
		assert.Equal(t, int32(1), middle2.invoked.Load())
	})

	t.Run("if chains are added and removed, chain should be updated", func(t *testing.T) {
		middle1 := newTestMiddle("test")
		middle2 := newTestMiddle("test2")
		h := New()
		h.Add(Spec{
			Component:      middle1.comp,
			Implementation: middle1.item.Middleware,
		})

		var invoked int
		root := nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { invoked++ })
		handler := h.BuildPipelineFromSpec("test", &config.PipelineSpec{Handlers: []config.HandlerSpec{
			{Name: "test", Type: "middleware.http.fakemw", Version: "v1"},
			{Name: "test2", Type: "middleware.http.fakemw", Version: ""},
		}})(root)

		assert.Equal(t, 0, invoked)
		assert.Equal(t, int32(0), middle1.invoked.Load())
		assert.Equal(t, int32(0), middle2.invoked.Load())
		handler.ServeHTTP(nil, nil)
		assert.Equal(t, 1, invoked)
		assert.Equal(t, int32(1), middle1.invoked.Load())
		assert.Equal(t, int32(0), middle2.invoked.Load())

		h.Add(Spec{
			Component:      middle2.comp,
			Implementation: middle2.item.Middleware,
		})
		handler.ServeHTTP(nil, nil)
		assert.Equal(t, 2, invoked)
		assert.Equal(t, int32(2), middle1.invoked.Load())
		assert.Equal(t, int32(1), middle2.invoked.Load())

		h.Remove("test3")
		handler.ServeHTTP(nil, nil)
		assert.Equal(t, 3, invoked)
		assert.Equal(t, int32(3), middle1.invoked.Load())
		assert.Equal(t, int32(2), middle2.invoked.Load())

		h.Remove("test")
		handler.ServeHTTP(nil, nil)
		assert.Equal(t, 4, invoked)
		assert.Equal(t, int32(3), middle1.invoked.Load())
		assert.Equal(t, int32(3), middle2.invoked.Load())

		h.Remove("test2")
		handler.ServeHTTP(nil, nil)
		assert.Equal(t, 5, invoked)
		assert.Equal(t, int32(3), middle1.invoked.Load())
		assert.Equal(t, int32(3), middle2.invoked.Load())

		h.Add(Spec{
			Component:      middle1.comp,
			Implementation: middle1.item.Middleware,
		})
		h.Add(Spec{
			Component:      middle2.comp,
			Implementation: middle2.item.Middleware,
		})
		handler.ServeHTTP(nil, nil)
		assert.Equal(t, 6, invoked)
		assert.Equal(t, int32(4), middle1.invoked.Load())
		assert.Equal(t, int32(4), middle2.invoked.Load())
	})
}
