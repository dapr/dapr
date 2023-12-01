/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/stretchr/testify/require"
)

// Subset of data returned by the metadata endpoint.
type metadataRes struct {
	ActorRuntime struct {
		RuntimeStatus string `json:"runtimeStatus"`
		ActiveActors  []struct {
			Type  string `json:"type"`
			Count int    `json:"count"`
		} `json:"activeActors"`
		HostReady bool   `json:"hostReady"`
		Placement string `json:"placement"`
	} `json:"actorRuntime"`
}

func getMetadata(t require.TestingT, ctx context.Context, client *http.Client, port int) (res metadataRes) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/v1.0/metadata", port), nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	err = json.NewDecoder(resp.Body).Decode(&res)
	require.NoError(t, err)

	return res
}
