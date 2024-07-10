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

package helm

import (
	"context"
	"encoding/json"
	"github.com/dapr/dapr/tests/integration/framework/process/helmtpl"
	"strings"
	"testing"
	"time"

	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	suite.Register(new(helm))
}

// basic tests the operator's ListCompontns API.
type helm struct {
	helm *helmtpl.Helm
}

func (b *helm) Setup(t *testing.T) []framework.Option {
	b.helm = helmtpl.New(t)
}

func (b *helm) Run(t *testing.T, ctx context.Context) {

	t.Run("LIST", func(t *testing.T) {
		var resp *operatorv1.ListComponentResponse
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var err error
			resp, err = client.ListComponents(ctx, &operatorv1.ListComponentsRequest{Namespace: "default"})
			require.NoError(t, err)
			assert.Len(c, resp.GetComponents(), 2)
		}, time.Second*20, time.Millisecond*10)

		b1, err := json.Marshal(b.comp1)
		require.NoError(t, err)
		b2, err := json.Marshal(b.comp2)
		require.NoError(t, err)

		if strings.Contains(string(resp.GetComponents()[0]), "mycomponent") {
			assert.JSONEq(t, string(b1), string(resp.GetComponents()[0]))
			assert.JSONEq(t, string(b2), string(resp.GetComponents()[1]))
		} else {
			assert.JSONEq(t, string(b1), string(resp.GetComponents()[1]))
			assert.JSONEq(t, string(b2), string(resp.GetComponents()[0]))
		}
	})
}
