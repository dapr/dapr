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
	suite.Register(&canstragglerdone{stragglerbody{name: "canstragglerdone", orphanFails: false}})
}

// canstragglerdone is canstraggler with an orphan that SUCCEEDS: its result
// carries the previous scheduling's execution id and must not become the new
// generation's output. Pre-fix the output was "done-first".
type canstragglerdone struct{ stragglerbody }

func (c *canstragglerdone) Setup(t *testing.T) []framework.Option { return c.setup(t) }

func (c *canstragglerdone) Run(t *testing.T, ctx context.Context) { c.run(t, ctx) }
