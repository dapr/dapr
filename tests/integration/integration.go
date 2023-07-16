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

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/binary"
	"github.com/dapr/dapr/tests/integration/suite"

	_ "github.com/dapr/dapr/tests/integration/suite/daprd"
	_ "github.com/dapr/dapr/tests/integration/suite/healthz"
	_ "github.com/dapr/dapr/tests/integration/suite/metadata"
	_ "github.com/dapr/dapr/tests/integration/suite/ports"
)

func RunIntegrationTests(t *testing.T) {
	binary.BuildAll(t)

	for name, tcase := range suite.All(t) {
		t.Run(name, func(t *testing.T) {
			t.Logf("setting up test case")
			options := tcase.Setup(t)

			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
			t.Cleanup(cancel)

			f := framework.Run(t, ctx, options...)

			t.Run("run", func(t *testing.T) {
				t.Log("running test case")
				tcase.Run(t, ctx)
			})

			t.Log("cleaning up framework")
			f.Cleanup(t)

			t.Log("done")
		})
	}
}
