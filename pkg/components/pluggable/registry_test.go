/*
Copyright 2022 The Dapr Authors
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
	warnFCalled atomic.Int64
	infoFCalled atomic.Int64
}

func (f *fakeLogger) Warnf(format string, args ...interface{}) {
	f.warnFCalled.Add(1)
}

func (f *fakeLogger) Infof(format string, args ...interface{}) {
	f.infoFCalled.Add(1)
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
	t.Run("register func should warnf when no registry exists for the given pluggable component", func(t *testing.T) {
		fakeLog := &fakeLogger{}
		revert := setLogger(fakeLog)
		defer revert()
		registries = make(map[components.PluggableType]func(components.Pluggable))

		assert.Equal(t, 0, Register(components.Pluggable{}))

		assert.Equal(t, int64(1), fakeLog.warnFCalled.Load())
	})

	t.Run("register func should call register func for the given pluggable component type", func(t *testing.T) {
		fakeLog := &fakeLogger{}
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
		assert.Equal(t, 1, Register(components.Pluggable{Type: components.State}))

		assert.Zero(t, fakeLog.warnFCalled.Load())
		assert.Equal(t, int64(1), fakeLog.infoFCalled.Load())
		assert.Equal(t, 0, mapCalled)
		assert.Equal(t, 1, registerCalled)
	})
}
