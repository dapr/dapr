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

package pluggable

import (
	"sync/atomic"
	"testing"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/kit/logger"

	"github.com/stretchr/testify/assert"
)

type fakeLogger struct {
	logger.Logger
	fatalFCalled atomic.Int32
}

func (f *fakeLogger) Fatalf(format string, args ...interface{}) {
	f.fatalFCalled.Add(1)
	panic("")
}

// setLogger sets the current package logger.
func setLogger(logger logger.Logger) (revert func()) {
	original := log
	log = logger
	return func() {
		log = original
	}
}

func TestRegisterFunc(t *testing.T) {
	t.Run("register func should fatalf when no registry exists for the given pluggable component", func(t *testing.T) {
		fakeLog := &fakeLogger{
			fatalFCalled: atomic.Int32{},
		}
		revert := setLogger(fakeLog)
		defer revert()
		registries = make(map[components.PluggableType]func(components.Pluggable))

		assert.Panics(t, func() { MustRegister(components.Pluggable{}) })

		assert.Equal(t, int32(1), fakeLog.fatalFCalled.Load())
	})

	t.Run("register func should call register func for the given pluggable component type", func(t *testing.T) {
		fakeLog := &fakeLogger{
			fatalFCalled: atomic.Int32{},
		}
		revert := setLogger(fakeLog)
		defer revert()
		registries = make(map[components.PluggableType]func(components.Pluggable))

		mapCalled, registerCalled := 0, 0
		AddRegistryFor(components.State, func(func(logger.Logger) state.Store, ...string) {
			registerCalled++
		}, func(logger.Logger, components.Pluggable) state.Store {
			mapCalled++
			var fake any
			return fake.(state.Store)
		})
		assert.NotPanics(t, func() { MustRegister(components.Pluggable{Type: components.State}) })

		assert.Zero(t, fakeLog.fatalFCalled.Load())
		assert.Equal(t, 1, mapCalled)
		assert.Equal(t, 1, registerCalled)
	})
}
