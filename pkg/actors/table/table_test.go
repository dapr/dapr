/*
Copyright 2025 The Dapr Authors
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

package table_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/actors/internal/reentrancystore"
	"github.com/dapr/dapr/pkg/actors/table"
)

func Test_GetOrCreate_NotRegistered(t *testing.T) {
	tble := table.New(table.Options{
		ReentrancyStore: reentrancystore.New(),
	})

	_, err := tble.GetOrCreate("test1", "1")
	require.Error(t, err)
	assert.ErrorIs(t, err, actorerrors.ErrCreatingActor)
}
