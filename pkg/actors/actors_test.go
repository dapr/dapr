/*
Copyright 2024 The Dapr Authors
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

package actors

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/modes"
)

func TestHostValidation(t *testing.T) {
	t.Parallel()

	t.Run("kubernetes mode with mTLS, missing namespace", func(t *testing.T) {
		err := ValidateHostEnvironment(true, modes.KubernetesMode, "")
		require.Error(t, err)
	})

	t.Run("kubernetes mode without mTLS, missing namespace", func(t *testing.T) {
		err := ValidateHostEnvironment(false, modes.KubernetesMode, "")
		require.NoError(t, err)
	})

	t.Run("kubernetes mode with mTLS and namespace", func(t *testing.T) {
		err := ValidateHostEnvironment(true, modes.KubernetesMode, "default")
		require.NoError(t, err)
	})

	t.Run("self hosted mode with mTLS, missing namespace", func(t *testing.T) {
		err := ValidateHostEnvironment(true, modes.StandaloneMode, "")
		require.NoError(t, err)
	})

	t.Run("self hosted mode without mTLS, missing namespace", func(t *testing.T) {
		err := ValidateHostEnvironment(false, modes.StandaloneMode, "")
		require.NoError(t, err)
	})
}
