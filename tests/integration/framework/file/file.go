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

package file

import (
	"crypto/rand"
	"math/big"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func Names(t *testing.T, num int) []string {
	require.GreaterOrEqual(t, num, 1)

	names := make([]string, num)
	for i := range num {
		names[i] = name(t)
	}

	return names
}

func Paths(t *testing.T, num int) []string {
	require.GreaterOrEqual(t, num, 1)

	dir := t.TempDir()
	names := make([]string, num)
	for i := range num {
		names[i] = filepath.Join(dir, name(t))
	}

	return names
}

func name(t *testing.T) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	bytes := make([]byte, 10)
	_, err := rand.Read(bytes)
	require.NoError(t, err)

	for i := range bytes {
		j, err := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		require.NoError(t, err)
		bytes[i] = letters[j.Int64()]
	}

	return string(bytes)
}
