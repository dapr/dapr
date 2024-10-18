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

package options

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Run("don't error if only using defaults", func(t *testing.T) {
		_, err := New([]string{})
		require.NoError(t, err)
	})

	t.Run("error when kubeconfig is set and using defaults", func(t *testing.T) {
		_, err := New([]string{
			"--kubeconfig=" + t.TempDir(),
		})
		require.Error(t, err)
	})

	t.Run("don't error when mode set to kubernetes", func(t *testing.T) {
		_, err := New([]string{
			"--mode=kubernetes",
		})
		require.NoError(t, err)
	})

	t.Run("error when mode set to standalone and kubeconfig is set", func(t *testing.T) {
		_, err := New([]string{
			"--mode=standalone",
			"--kubeconfig=" + t.TempDir(),
		})
		require.Error(t, err)
	})

	t.Run("don't error when mode set to kubernetes and kubeconfig is set", func(t *testing.T) {
		_, err := New([]string{
			"--mode=kubernetes",
			"--kubeconfig=" + t.TempDir(),
		})
		require.NoError(t, err)
	})
}
