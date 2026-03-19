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

package signing

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(largepayload))
}

type largepayload struct {
	sentry *sentry.Sentry
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (l *largepayload) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)
	l.sentry = sentry

	l.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)

	place := placement.New(t, placement.WithSentry(t, sentry))
	sched := scheduler.New(t, scheduler.WithSentry(sentry))

	l.daprd = daprd.New(t,
		daprd.WithSentry(t, sentry),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithScheduler(sched),
		daprd.WithResourceFiles(l.db.GetComponent(t)),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, l.db, place, sched, l.daprd),
	}
}

type orderItem struct {
	ProductID string  `json:"productId"`
	Quantity  int     `json:"quantity"`
	Price     float64 `json:"price"`
}

type order struct {
	CustomerID string      `json:"customerId"`
	Items      []orderItem `json:"items"`
	Notes      string      `json:"notes"`
}

type orderResult struct {
	OrderID string  `json:"orderId"`
	Total   float64 `json:"total"`
}

func (l *largepayload) Run(t *testing.T, ctx context.Context) {
	l.daprd.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-order", func(ctx *dworkflow.WorkflowContext) (any, error) {
		var input order
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		var result orderResult
		if err := ctx.CallActivity("process-order", dworkflow.WithActivityInput(input)).Await(&result); err != nil {
			return nil, err
		}
		return result, nil
	})
	reg.AddActivityN("process-order", func(ctx dworkflow.ActivityContext) (any, error) {
		var input order
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		var total float64
		for _, item := range input.Items {
			total += float64(item.Quantity) * item.Price
		}
		return orderResult{
			OrderID: "ORD-12345",
			Total:   total,
		}, nil
	})

	client := dworkflow.NewClient(l.daprd.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	input := order{
		CustomerID: "CUST-9999",
		Items: []orderItem{
			{ProductID: "PROD-A", Quantity: 3, Price: 19.99},
			{ProductID: "PROD-B", Quantity: 1, Price: 149.99},
			{ProductID: "PROD-C", Quantity: 10, Price: 4.50},
		},
		// Include a large string field to exercise signing over non-trivial payloads.
		Notes: strings.Repeat("This is a detailed order note. ", 100),
	}

	id, err := client.ScheduleWorkflow(ctx, "sign-order", dworkflow.WithInput(input))
	require.NoError(t, err)

	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, dworkflow.StatusCompleted, meta.RuntimeStatus)

	fworkflow.VerifySignatureChain(t, ctx, l.db, id,
		l.sentry.CABundle().X509.TrustAnchors,
	)
	fworkflow.VerifyCertAppID(t, ctx, l.db, id, l.daprd.AppID())
}
