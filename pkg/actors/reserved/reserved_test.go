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

package reserved

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsInternalActorType(t *testing.T) {
	tests := map[string]bool{
		"dapr.internal":                         true,
		"dapr.internal.":                        true,
		"dapr.internal.default.app.workflow":    true,
		"dapr.internal.default.app.retentioner": true,
		"dapr.internalx":                        false,
		"dapr.internals.foo":                    false,
		"Dapr.Internal.default.app.workflow":    false,
		"dapr":                                  false,
		"":                                      false,
		"TransactionActor":                      false,
		"my.dapr.internal.actor":                false,
	}
	for actorType, want := range tests {
		t.Run(actorType, func(t *testing.T) {
			assert.Equal(t, want, IsInternalActorType(actorType))
		})
	}
}
