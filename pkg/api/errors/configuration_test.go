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

package errors

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kiterrors "github.com/dapr/kit/errors"
)

func TestConfigurationErrors(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantHTTPCode int
		wantMsgPart  string
	}{
		{"NotConfigured", Configuration("mystore").NotConfigured(), http.StatusInternalServerError, "not configured"},
		{"NotFound", Configuration("mystore").NotFound(), http.StatusBadRequest, "mystore"},
		{"GetFailed", Configuration("mystore").GetFailed([]string{"key1"}, "boom"), http.StatusInternalServerError, "failed to get"},
		{"SubscribeFailed", Configuration("mystore").SubscribeFailed([]string{"key1"}, "boom"), http.StatusInternalServerError, "failed to subscribe"},
		{"UnsubscribeFailed", Configuration("mystore").UnsubscribeFailed("sub-1", "boom"), http.StatusInternalServerError, "failed to unsubscribe"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, tc.err)

			kitErr, ok := kiterrors.FromError(tc.err)
			require.True(t, ok, "expected a standardized kit error")

			assert.Equal(t, tc.wantHTTPCode, kitErr.HTTPStatusCode())
			assert.Contains(t, kitErr.Error(), tc.wantMsgPart)
		})
	}
}
