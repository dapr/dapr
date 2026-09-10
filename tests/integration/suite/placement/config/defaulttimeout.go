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
	suite.Register(new(defaultTimeout))
}

// defaultTimeout asserts the Config RPC advertises the default dissemination
// timeout when no flag is given.
type defaultTimeout struct {
	place *placement.Placement
}

func (d *defaultTimeout) Setup(t *testing.T) []framework.Option {
	d.place = placement.New(t)

	return []framework.Option{
		framework.WithProcesses(d.place),
	}
}

func (d *defaultTimeout) Run(t *testing.T, ctx context.Context) {
	d.place.WaitUntilRunning(t, ctx)

	resp, err := d.place.Client(t, ctx).Config(ctx, new(v1pb.ConfigRequest))
	require.NoError(t, err)
	require.NotNil(t, resp.GetDisseminateTimeout())
	assert.Equal(t, time.Second*8, resp.GetDisseminateTimeout().AsDuration())
}
