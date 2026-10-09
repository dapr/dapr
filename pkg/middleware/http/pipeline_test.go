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
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/middleware"
	"github.com/dapr/dapr/pkg/middleware/store"
)

func TestPipeline_http(t *testing.T) {
	t.Run("if chain already built, should use chain", func(t *testing.T) {
		store := store.New[middleware.HTTP]("test")
		middle1 := newTestMiddle("test")
		middle2 := newTestMiddle("test2")
		store.Add(middle1.item)
		store.Add(middle2.item)
		p := newPipeline("test", store, &config.PipelineSpec{Handlers: []config.HandlerSpec{
			{Name: "test", Type: "middleware.http.fakemw", Version: "v1"},
			{Name: "test2", Type: "middleware.http.fakemw", Version: ""},
		}})

		var invoked int
		root := nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { invoked++ })
		handler := p.http()(root)

		assert.Equal(t, 0, invoked)
		assert.Equal(t, int32(0), middle1.invoked.Load())
		assert.Equal(t, int32(0), middle2.invoked.Load())
		handler.ServeHTTP(nil, nil)
		assert.Equal(t, 1, invoked)
		assert.Equal(t, int32(1), middle1.invoked.Load())
		assert.Equal(t, int32(1), middle2.invoked.Load())
	})

	t.Run("if chain is added after, should use chain", func(t *testing.T) {
		store := store.New[middleware.HTTP]("test")
		middle1 := newTestMiddle("test")
		middle2 := newTestMiddle("test2")
		p := newPipeline("test", store, &config.PipelineSpec{Handlers: []config.HandlerSpec{
			{Name: "test", Type: "middleware.http.fakemw", Version: "v1"},
			{Name: "test2", Type: "middleware.http.fakemw", Version: ""},
		}})

		var invoked int
		root := nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { invoked++ })
		handler := p.http()(root)

		assert.Equal(t, 0, invoked)
		assert.Equal(t, int32(0), middle1.invoked.Load())
		assert.Equal(t, int32(0), middle2.invoked.Load())
		handler.ServeHTTP(nil, nil)
		assert.Equal(t, 1, invoked)
		assert.Equal(t, int32(0), middle1.invoked.Load())
		assert.Equal(t, int32(0), middle2.invoked.Load())

		store.Add(middle1.item)
		store.Add(middle2.item)
		p.buildChain()
		handler.ServeHTTP(nil, nil)
		assert.Equal(t, 2, invoked)
		assert.Equal(t, int32(1), middle1.invoked.Load())
		assert.Equal(t, int32(1), middle2.invoked.Load())
	})

	t.Run("if chains are added and removed, chain should be updated", func(t *testing.T) {
		store := store.New[middleware.HTTP]("test")
		middle1 := newTestMiddle("test")
		middle2 := newTestMiddle("test2")
		store.Add(middle1.item)
		p := newPipeline("test", store, &config.PipelineSpec{Handlers: []config.HandlerSpec{
			{Name: "test", Type: "middleware.http.fakemw", Version: "v1"},
			{Name: "test2", Type: "middleware.http.fakemw", Version: ""},
		}})

		var invoked int
		root := nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { invoked++ })
		handler := p.http()(root)

		assert.Equal(t, 0, invoked)
		assert.Equal(t, int32(0), middle1.invoked.Load())
		assert.Equal(t, int32(0), middle2.invoked.Load())
		handler.ServeHTTP(nil, nil)
		assert.Equal(t, 1, invoked)
		assert.Equal(t, int32(1), middle1.invoked.Load())
		assert.Equal(t, int32(0), middle2.invoked.Load())

		store.Add(middle2.item)
		p.buildChain()
		handler.ServeHTTP(nil, nil)
		assert.Equal(t, 2, invoked)
		assert.Equal(t, int32(2), middle1.invoked.Load())
		assert.Equal(t, int32(1), middle2.invoked.Load())

		store.Remove("test3")
		p.buildChain()
		handler.ServeHTTP(nil, nil)
		assert.Equal(t, 3, invoked)
		assert.Equal(t, int32(3), middle1.invoked.Load())
		assert.Equal(t, int32(2), middle2.invoked.Load())

		store.Remove("test")
		p.buildChain()
		handler.ServeHTTP(nil, nil)
		assert.Equal(t, 4, invoked)
		assert.Equal(t, int32(3), middle1.invoked.Load())
		assert.Equal(t, int32(3), middle2.invoked.Load())

		store.Remove("test2")
		p.buildChain()
		handler.ServeHTTP(nil, nil)
		assert.Equal(t, 5, invoked)
		assert.Equal(t, int32(3), middle1.invoked.Load())
		assert.Equal(t, int32(3), middle2.invoked.Load())

		store.Add(middle1.item)
		store.Add(middle2.item)
		p.buildChain()
		handler.ServeHTTP(nil, nil)
		assert.Equal(t, 6, invoked)
		assert.Equal(t, int32(4), middle1.invoked.Load())
		assert.Equal(t, int32(4), middle2.invoked.Load())
	})
}

func TestPipeline_buildChain(t *testing.T) {
	t.Run("if spec is nil, root is called directly", func(t *testing.T) {
		store := store.New[middleware.HTTP]("test")
		middle := newTestMiddle("test")
		store.Add(middle.item)
		p := newPipeline("test", store, nil)
		p.buildChain()

		var called bool
		p.wrap(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { called = true })).ServeHTTP(nil, nil)
		assert.True(t, called)
		assert.Equal(t, int32(0), middle.invoked.Load())
	})

	t.Run("if spec handlers is empty, root is called directly", func(t *testing.T) {
		store := store.New[middleware.HTTP]("test")
		middle := newTestMiddle("test")
		store.Add(middle.item)
		for _, spec := range []*config.PipelineSpec{{Handlers: nil}, {Handlers: []config.HandlerSpec{}}} {
			p := newPipeline("test", store, spec)
			p.buildChain()

			var called bool
			p.wrap(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { called = true })).ServeHTTP(nil, nil)
			assert.True(t, called)
		}
		assert.Equal(t, int32(0), middle.invoked.Load())
	})

	t.Run("if spec references handler that does not exist, chain should just be root", func(t *testing.T) {
		store := store.New[middleware.HTTP]("test")
		p := newPipeline("test", store, &config.PipelineSpec{Handlers: []config.HandlerSpec{
			{Name: "test", Type: "middleware.http.fakemw", Version: "v1"},
		}})
		var called bool
		p.buildChain()
		chain := p.wrap(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { called = true }))
		assert.False(t, called)
		chain.ServeHTTP(nil, nil)
		assert.True(t, called)
	})

	t.Run("if spec references multiple handlers that does not exist, chain should just be root", func(t *testing.T) {
		store := store.New[middleware.HTTP]("test")
		p := newPipeline("test", store, &config.PipelineSpec{Handlers: []config.HandlerSpec{
			{Name: "test", Type: "middleware.http.fakemw", Version: "v1"},
			{Name: "test2", Type: "middleware.http.fakemw", Version: ""},
		}})

		var called bool
		p.buildChain()
		chain := p.wrap(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { called = true }))
		assert.False(t, called)
		chain.ServeHTTP(nil, nil)
		assert.True(t, called)
	})

	t.Run("if spec references handler that does exist, chain should include middleware", func(t *testing.T) {
		store := store.New[middleware.HTTP]("test")
		middle := newTestMiddle("test")
		store.Add(middle.item)
		p := newPipeline("test", store, &config.PipelineSpec{Handlers: []config.HandlerSpec{
			{Name: "test", Type: "middleware.http.fakemw", Version: "v1"},
		}})

		var invoked int
		p.buildChain()
		chain := p.wrap(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { invoked++ }))
		assert.Equal(t, 0, invoked)
		chain.ServeHTTP(nil, nil)
		assert.Equal(t, 1, invoked)
		assert.Equal(t, int32(1), middle.invoked.Load())
		chain.ServeHTTP(nil, nil)
		assert.Equal(t, 2, invoked)
		assert.Equal(t, int32(2), middle.invoked.Load())
	})

	t.Run("if spec references multiple handlers that exist, chain should include middlewares", func(t *testing.T) {
		store := store.New[middleware.HTTP]("test")
		middle1 := newTestMiddle("test")
		middle2 := newTestMiddle("test2")
		store.Add(middle1.item)
		store.Add(middle2.item)
		p := newPipeline("test", store, &config.PipelineSpec{Handlers: []config.HandlerSpec{
			{Name: "test", Type: "middleware.http.fakemw", Version: "v1"},
			{Name: "test2", Type: "middleware.http.fakemw", Version: "v1"},
		}})

		var invoked int
		p.buildChain()
		chain := p.wrap(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { invoked++ }))

		assert.Equal(t, 0, invoked)
		chain.ServeHTTP(nil, nil)
		assert.Equal(t, 1, invoked)
		assert.Equal(t, int32(1), middle1.invoked.Load())
		assert.Equal(t, int32(1), middle2.invoked.Load())
		chain.ServeHTTP(nil, nil)
		assert.Equal(t, 2, invoked)
		assert.Equal(t, int32(2), middle1.invoked.Load())
		assert.Equal(t, int32(2), middle2.invoked.Load())
	})

	t.Run("if spec references handler that doesn't exist, it shouldn't be used", func(t *testing.T) {
		store := store.New[middleware.HTTP]("test")
		middle1 := newTestMiddle("test")
		middle2 := newTestMiddle("test2")
		store.Add(middle1.item)
		p := newPipeline("test", store, &config.PipelineSpec{Handlers: []config.HandlerSpec{
			{Name: "test", Type: "middleware.http.fakemw", Version: "v1"},
			{Name: "test2", Type: "middleware.http.fakemw", Version: "v1"},
		}})

		var invoked int
		p.buildChain()
		chain := p.wrap(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { invoked++ }))

		assert.Equal(t, 0, invoked)
		chain.ServeHTTP(nil, nil)
		assert.Equal(t, 1, invoked)
		assert.Equal(t, int32(1), middle1.invoked.Load())
		assert.Equal(t, int32(0), middle2.invoked.Load())
		chain.ServeHTTP(nil, nil)
		assert.Equal(t, 2, invoked)
		assert.Equal(t, int32(2), middle1.invoked.Load())
		assert.Equal(t, int32(0), middle2.invoked.Load())
	})

	t.Run("if spec defaults version, should still be called", func(t *testing.T) {
		store := store.New[middleware.HTTP]("test")
		middle1 := newTestMiddle("test")
		middle2 := newTestMiddle("test2")
		store.Add(middle1.item)
		store.Add(middle2.item)
		p := newPipeline("test", store, &config.PipelineSpec{Handlers: []config.HandlerSpec{
			{Name: "test", Type: "middleware.http.fakemw", Version: "v1"},
			{Name: "test2", Type: "middleware.http.fakemw", Version: ""},
		}})

		var invoked int
		p.buildChain()
		chain := p.wrap(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { invoked++ }))

		assert.Equal(t, 0, invoked)
		chain.ServeHTTP(nil, nil)
		assert.Equal(t, 1, invoked)
		assert.Equal(t, int32(1), middle1.invoked.Load())
		assert.Equal(t, int32(1), middle2.invoked.Load())
		chain.ServeHTTP(nil, nil)
		assert.Equal(t, 2, invoked)
		assert.Equal(t, int32(2), middle1.invoked.Load())
		assert.Equal(t, int32(2), middle2.invoked.Load())
	})

	t.Run("if spec references different type, shouldn't be used", func(t *testing.T) {
		store := store.New[middleware.HTTP]("test")
		middle1 := newTestMiddle("test")
		middle2 := newTestMiddle("test2")
		store.Add(middle1.item)
		store.Add(middle2.item)
		p := newPipeline("test", store, &config.PipelineSpec{Handlers: []config.HandlerSpec{
			{Name: "test", Type: "middleware.http.fakemw", Version: "v1"},
			{Name: "test2", Type: "middleware.http.notfakemw", Version: "v1"},
		}})

		var invoked int
		p.buildChain()
		chain := p.wrap(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { invoked++ }))

		assert.Equal(t, 0, invoked)
		chain.ServeHTTP(nil, nil)
		assert.Equal(t, 1, invoked)
		assert.Equal(t, int32(1), middle1.invoked.Load())
		assert.Equal(t, int32(0), middle2.invoked.Load())
		chain.ServeHTTP(nil, nil)
		assert.Equal(t, 2, invoked)
		assert.Equal(t, int32(2), middle1.invoked.Load())
		assert.Equal(t, int32(0), middle2.invoked.Load())
	})

	t.Run("if spec references different name, shouldn't be used", func(t *testing.T) {
		store := store.New[middleware.HTTP]("test")
		middle1 := newTestMiddle("test")
		middle2 := newTestMiddle("test2")
		store.Add(middle1.item)
		store.Add(middle2.item)
		p := newPipeline("test", store, &config.PipelineSpec{Handlers: []config.HandlerSpec{
			{Name: "test", Type: "middleware.http.fakemw", Version: "v1"},
			{Name: "nottest2", Type: "middleware.http.fakemw", Version: "v1"},
		}})

		var invoked int
		p.buildChain()
		chain := p.wrap(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { invoked++ }))

		assert.Equal(t, 0, invoked)
		chain.ServeHTTP(nil, nil)
		assert.Equal(t, 1, invoked)
		assert.Equal(t, int32(1), middle1.invoked.Load())
		assert.Equal(t, int32(0), middle2.invoked.Load())
		chain.ServeHTTP(nil, nil)
		assert.Equal(t, 2, invoked)
		assert.Equal(t, int32(2), middle1.invoked.Load())
		assert.Equal(t, int32(0), middle2.invoked.Load())
	})
}

type testmiddle struct {
	item    store.Item[middleware.HTTP]
	comp    compapi.Component
	invoked atomic.Int32
	built   atomic.Int32
	wait    chan struct{}
}

func newTestMiddle(name string) *testmiddle {
	tm := &testmiddle{
		comp: compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: compapi.ComponentSpec{
				Type:    "middleware.http.fakemw",
				Version: "v1",
			},
		},
	}
	tm.wait = make(chan struct{})
	close(tm.wait)
	tm.item = store.Item[middleware.HTTP]{
		Metadata: store.Metadata{
			Name:    name,
			Type:    "middleware.http.fakemw",
			Version: "v1",
		},
		Middleware: func(next nethttp.Handler) nethttp.Handler {
			tm.built.Add(1)
			return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
				<-tm.wait
				tm.invoked.Add(1)
				next.ServeHTTP(w, r)
			})
		},
	}
	return tm
}

func TestPipeline_concurrentRoots(t *testing.T) {
	store := store.New[middleware.HTTP]("test")
	middle := newTestMiddle("test")
	store.Add(middle.item)
	mw := newPipeline("test", store, &config.PipelineSpec{Handlers: []config.HandlerSpec{
		{Name: "test", Type: "middleware.http.fakemw", Version: "v1"},
	}}).http()

	var wg sync.WaitGroup
	for i := range 64 {
		wg.Go(func() {
			var ran atomic.Int32
			mw(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) {
				ran.Add(1)
			})).ServeHTTP(nil, nil)
			assert.Equal(t, int32(1), ran.Load(), "root %d must run exactly once, for its own request", i)
		})
	}
	wg.Wait()
	assert.Equal(t, int32(64), middle.invoked.Load())
}

func TestPipeline_cachedChain(t *testing.T) {
	t.Run("long-lived root should build chain once", func(t *testing.T) {
		store := store.New[middleware.HTTP]("test")
		middle1 := newTestMiddle("test")
		middle2 := newTestMiddle("test2")
		store.Add(middle1.item)
		store.Add(middle2.item)
		p := newPipeline("test", store, &config.PipelineSpec{Handlers: []config.HandlerSpec{
			{Name: "test", Type: "middleware.http.fakemw", Version: "v1"},
			{Name: "test2", Type: "middleware.http.fakemw", Version: "v1"},
		}})

		var invoked int
		handler := p.http()(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { invoked++ }))
		for range 100 {
			handler.ServeHTTP(nil, nil)
		}
		assert.Equal(t, 100, invoked)
		assert.Equal(t, int32(100), middle1.invoked.Load())
		assert.Equal(t, int32(100), middle2.invoked.Load())
		assert.Equal(t, int32(1), middle1.built.Load())
		assert.Equal(t, int32(1), middle2.built.Load())
	})

	t.Run("rebuilding the pipeline should rebuild the chain once on next request", func(t *testing.T) {
		store := store.New[middleware.HTTP]("test")
		middle1 := newTestMiddle("test")
		middle2 := newTestMiddle("test2")
		store.Add(middle1.item)
		p := newPipeline("test", store, &config.PipelineSpec{Handlers: []config.HandlerSpec{
			{Name: "test", Type: "middleware.http.fakemw", Version: "v1"},
			{Name: "test2", Type: "middleware.http.fakemw", Version: "v1"},
		}})

		var invoked int
		handler := p.http()(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { invoked++ }))
		for range 10 {
			handler.ServeHTTP(nil, nil)
		}
		assert.Equal(t, int32(1), middle1.built.Load())
		assert.Equal(t, int32(0), middle2.built.Load())

		store.Add(middle2.item)
		p.buildChain()
		assert.Equal(t, int32(1), middle1.built.Load())
		assert.Equal(t, int32(0), middle2.built.Load())
		for range 10 {
			handler.ServeHTTP(nil, nil)
		}
		assert.Equal(t, 20, invoked)
		assert.Equal(t, int32(20), middle1.invoked.Load())
		assert.Equal(t, int32(10), middle2.invoked.Load())
		assert.Equal(t, int32(2), middle1.built.Load())
		assert.Equal(t, int32(1), middle2.built.Load())

		store.Remove("test")
		p.buildChain()
		for range 10 {
			handler.ServeHTTP(nil, nil)
		}
		assert.Equal(t, 30, invoked)
		assert.Equal(t, int32(20), middle1.invoked.Load())
		assert.Equal(t, int32(20), middle2.invoked.Load())
		assert.Equal(t, int32(2), middle1.built.Load())
		assert.Equal(t, int32(2), middle2.built.Load())
	})

	t.Run("concurrent requests and rebuilds on one root", func(t *testing.T) {
		store := store.New[middleware.HTTP]("test")
		middle := newTestMiddle("test")
		store.Add(middle.item)
		p := newPipeline("test", store, &config.PipelineSpec{Handlers: []config.HandlerSpec{
			{Name: "test", Type: "middleware.http.fakemw", Version: "v1"},
		}})

		var ran atomic.Int32
		handler := p.http()(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { ran.Add(1) }))

		var wg sync.WaitGroup
		for range 32 {
			wg.Go(func() {
				for range 100 {
					handler.ServeHTTP(nil, nil)
				}
			})
		}
		for range 8 {
			wg.Go(p.buildChain)
		}
		wg.Wait()
		assert.Equal(t, int32(3200), ran.Load())
		assert.Equal(t, int32(3200), middle.invoked.Load())

		handler.ServeHTTP(nil, nil)
		built := middle.built.Load()
		for range 10 {
			handler.ServeHTTP(nil, nil)
		}
		assert.Equal(t, built, middle.built.Load())
	})

	t.Run("concurrent requests across many long-lived roots", func(t *testing.T) {
		store := store.New[middleware.HTTP]("test")
		middle := newTestMiddle("test")
		store.Add(middle.item)
		mw := newPipeline("test", store, &config.PipelineSpec{Handlers: []config.HandlerSpec{
			{Name: "test", Type: "middleware.http.fakemw", Version: "v1"},
		}}).http()

		var wg sync.WaitGroup
		ran := make([]atomic.Int32, 32)
		for i := range ran {
			handler := mw(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { ran[i].Add(1) }))
			for range 4 {
				wg.Go(func() {
					for range 50 {
						handler.ServeHTTP(nil, nil)
					}
				})
			}
		}
		wg.Wait()
		for i := range ran {
			assert.Equal(t, int32(200), ran[i].Load(), "root %d must only run for its own requests", i)
		}
		assert.Equal(t, int32(32*200), middle.invoked.Load())
		assert.Equal(t, int32(32), middle.built.Load())
	})
}

func BenchmarkPipeline_longLivedRoot(b *testing.B) {
	store := store.New[middleware.HTTP]("test")
	store.Add(newTestMiddle("test").item)
	store.Add(newTestMiddle("test2").item)
	handler := newPipeline("test", store, &config.PipelineSpec{Handlers: []config.HandlerSpec{
		{Name: "test", Type: "middleware.http.fakemw", Version: "v1"},
		{Name: "test2", Type: "middleware.http.fakemw", Version: "v1"},
	}}).http()(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) {}))

	b.ReportAllocs()
	for b.Loop() {
		handler.ServeHTTP(nil, nil)
	}
}
