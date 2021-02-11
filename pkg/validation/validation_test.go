// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package validation

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidationForKubernetes(t *testing.T) {
	t.Run("invalid length", func(t *testing.T) {
		id := ""
		for i := 0; i < 64; i++ {
			id += "a"
		}
		err := ValidateKubernetesAppID(id)
		assert.Error(t, err)
	})

	t.Run("valid id", func(t *testing.T) {
		id := "my-app-id"
		err := ValidateKubernetesAppID(id)
		assert.NoError(t, err)
	})

	t.Run("invalid char: .", func(t *testing.T) {
		id := "my-app-id.app"
		err := ValidateKubernetesAppID(id)
		assert.Error(t, err)
	})

	t.Run("invalid chars space", func(t *testing.T) {
		id := "my-app-id app"
		err := ValidateKubernetesAppID(id)
		assert.Error(t, err)
	})

	t.Run("invalid empty", func(t *testing.T) {
		id := ""
		err := ValidateKubernetesAppID(id)
		assert.Regexp(t, "value for the dapr.io/app-id annotation is empty", err.Error())
	})
}
