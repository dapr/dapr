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

package loadbalance

import (
	"context"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(&canstraggler{stragglerbody{name: "canstraggler", orphanFails: true}})
}

// canstraggler verifies that a FAILED activity orphaned by ContinueAsNew
// cannot resolve the new generation's task of the same id. Pre-fix the
// orphan's failure was applied to that task and the workflow ended FAILED.
type canstraggler struct{ stragglerbody }

func (c *canstraggler) Setup(t *testing.T) []framework.Option { return c.setup(t) }

func (c *canstraggler) Run(t *testing.T, ctx context.Context) { c.run(t, ctx) }
