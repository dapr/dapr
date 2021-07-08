// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

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
