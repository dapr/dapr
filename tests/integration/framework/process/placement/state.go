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

package placement

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/client"
)

type TableState struct {
	Tables map[string]*Table `json:"tables"`
}

type Table struct {
	Hosts   []Host `json:"hosts"`
	Version uint64 `json:"version"`
}

type Host struct {
	Name      string   `json:"name"`
	ID        string   `json:"id"`
	APIVLevel int      `json:"api_level"`
	Namespace string   `json:"namespace"`
	Entities  []string `json:"entities"`
}

func (p *Placement) PlacementTables(t *testing.T, ctx context.Context) *TableState {
	t.Helper()

	client := client.HTTP(t)
	req, err := http.NewRequestWithContext(ctx,
		http.MethodGet,
		fmt.Sprintf("http://localhost:%d/placement/state", p.HealthzPort()),
		nil,
	)
	require.NoError(t, err)

	res, err := client.Do(req)
	require.NoError(t, err)

	tables := TableState{
		Tables: make(map[string]*Table),
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&tables))
	require.NoError(t, res.Body.Close())

	return &tables
}
