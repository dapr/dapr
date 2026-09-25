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

package selfhosted

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/cert"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(disabled))
}

// disabled ensures rotation is off by default: even a near-expiry root CA is
// not rotated unless -rotation-enabled is set, so an operator-provided root
// CA is never replaced by a sentry-generated one.
type disabled struct {
	sentry *procsentry.Sentry
}

func (d *disabled) Setup(t *testing.T) []framework.Option {
	// Near-expiry root CA which would trigger rotation immediately were
	// rotation enabled. Note the absence of WithRotationEnabled.
	d.sentry = procsentry.New(t,
		procsentry.WithTrustDomain(trustDomain),
		procsentry.WithCABundle(cert.GenerateCABundle(t, trustDomain, time.Hour)),
		procsentry.WithRotationCheckInterval(time.Second),
	)

	return []framework.Option{framework.WithProcesses(d.sentry)}
}

func (d *disabled) Run(t *testing.T, ctx context.Context) {
	d.sentry.WaitUntilRunning(t, ctx)

	assert.Never(t, func() bool {
		_, ok := d.sentry.RotationState()
		return ok
	}, time.Second*3, time.Millisecond*10, "rotation must not start when not enabled")

	anchors := cert.DecodePEM(t, d.sentry.DiskTrustAnchorsPEM(t))
	assert.Len(t, anchors, 1, "trust anchors must contain only the original root CA")
}
