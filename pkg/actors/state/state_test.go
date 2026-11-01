/*
Copyright 2026 The Dapr Authors
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

package state

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	inmemory "github.com/dapr/components-contrib/state/in-memory"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/kit/logger"
)

// Test_stateStore verifies the actor state store is resolved from the
// component store on every call, so hot reloading the store (add, replace,
// remove) is observed without reconstructing the state facade.
func Test_stateStore(t *testing.T) {
	cs := compstore.New()
	s := &state{compStore: cs}

	_, _, err := s.stateStore()
	require.ErrorIs(t, err, messages.ErrActorRuntimeNotFound)

	require.NoError(t, cs.AddStateStoreActor("mystore", inmemory.NewInMemoryStateStore(logger.NewLogger(t.Name()))))
	name, store, err := s.stateStore()
	require.NoError(t, err)
	assert.Equal(t, "mystore", name)
	assert.NotNil(t, store)

	cs.DeleteStateStore("mystore")
	_, _, err = s.stateStore()
	require.ErrorIs(t, err, messages.ErrActorRuntimeNotFound)

	// A store under a different name is picked up by name transparently.
	require.NoError(t, cs.AddStateStoreActor("otherstore", inmemory.NewInMemoryStateStore(logger.NewLogger(t.Name()))))
	name, _, err = s.stateStore()
	require.NoError(t, err)
	assert.Equal(t, "otherstore", name)
}
