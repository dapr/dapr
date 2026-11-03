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

package connleak

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	dactors "github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(connleak))
}

// connleak stops the placement service and checks that the daprd goroutine
// count stays flat while daprd retries it. Each failed attempt dials a new
// connection. The dial succeeds and only the stream fails, so a connection
// which is not closed keeps its goroutines and keeps redialing. Without the
// fix the count grows by about 8 per second.
type connleak struct {
	actors *dactors.Actors
}

func (c *connleak) Setup(t *testing.T) []framework.Option {
	c.actors = dactors.New(t,
		dactors.WithActorTypes("myactor"),
		dactors.WithPlacementService(),
	)

	return []framework.Option{
		framework.WithProcesses(c.actors),
	}
}

func (c *connleak) Run(t *testing.T, ctx context.Context) {
	c.actors.WaitUntilRunning(t, ctx)

	goroutines := func() float64 {
		return c.actors.Daprd().Metrics(t, ctx).All()["go_goroutines"]
	}

	c.actors.Placement().Cleanup(t)

	// Let daprd drop the stream and make its first failed attempts.
	time.Sleep(time.Millisecond * 500)
	base := goroutines()

	assert.Never(t, func() bool {
		return goroutines() > base+6
	}, time.Second*2, time.Millisecond*100, "goroutines grew while placement is down, base %v", base)
}
