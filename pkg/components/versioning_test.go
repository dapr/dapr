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

package components_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/components"
)

func TestIsInitialVersion(t *testing.T) {
	tests := map[string]struct {
		version string
		initial bool
	}{
		"empty version":       {version: "", initial: true},
		"unstable":            {version: "v0", initial: true},
		"first stable":        {version: "v1", initial: true},
		"second stable":       {version: "v2", initial: false},
		"unstable upper":      {version: "V0", initial: true},
		"first stable upper":  {version: "V1", initial: true},
		"second stable upper": {version: "V2", initial: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			actual := components.IsInitialVersion(tc.version)
			assert.Equal(t, tc.initial, actual)
		})
	}
}
