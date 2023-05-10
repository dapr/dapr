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
	"reflect"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/suite"
	_ "github.com/dapr/dapr/tests/integration/suite/healthz"
	_ "github.com/dapr/dapr/tests/integration/suite/ports"
)

func RunIntegrationTests(t *testing.T) {
	// Parallelise the integration tests, but don't run more than 3 at once.
	guard := make(chan struct{}, 3)

	for _, tcase := range suite.All() {
		tcase := tcase
		t.Run(reflect.TypeOf(tcase).Elem().Name(), func(t *testing.T) {
			t.Parallel()

			guard <- struct{}{}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			t.Log("setting up test case")
			options := tcase.Setup(t)

			t.Log("running daprd")
			daprd := framework.RunDaprd(t, ctx, options...)

			t.Log("running test case")
			tcase.Run(t, daprd)

			t.Log("cleaning up test case")
			daprd.Cleanup(t)

			t.Log("done")
			<-guard
		})
	}
}
