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

package config

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(custom))
}

// custom asserts the Config RPC advertises the dissemination timeout given
// with --disseminate-timeout.
type custom struct {
	place *placement.Placement
}

func (c *custom) Setup(t *testing.T) []framework.Option {
	c.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*11),
	)

	return []framework.Option{
		framework.WithProcesses(c.place),
	}
}

func (c *custom) Run(t *testing.T, ctx context.Context) {
	c.place.WaitUntilRunning(t, ctx)

	resp, err := c.place.Client(t, ctx).Config(ctx, new(v1pb.ConfigRequest))
	require.NoError(t, err)
	require.NotNil(t, resp.GetDisseminateTimeout())
	assert.Equal(t, time.Second*11, resp.GetDisseminateTimeout().AsDuration())
}
