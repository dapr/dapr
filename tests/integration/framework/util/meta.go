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

package util

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtpbv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

type metaResponse struct {
	Components []*rtpbv1.RegisteredComponents `json:"components,omitempty"`
}

func GetMetaComponents(t require.TestingT, ctx context.Context, client *http.Client, port int) []*rtpbv1.RegisteredComponents {
	return getMeta(t, ctx, client, port).Components
}

func getMeta(t require.TestingT, ctx context.Context, client *http.Client, port int) metaResponse {
	metaURL := fmt.Sprintf("http://localhost:%d/v1.0/metadata", port)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, nil)
	require.NoError(t, err)

	var meta metaResponse
	resp, err := client.Do(req)
	//nolint:testifylint
	if assert.NoError(t, err) {
		defer resp.Body.Close()
		//nolint:testifylint
		assert.NoError(t, json.NewDecoder(resp.Body).Decode(&meta))
	}

	return meta
}
