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

package encryption

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(crossapp))
	suite.Register(new(crossappmismatch))
}

// crossAppRegistries returns the workflow app's registry, calling "charge" on
// the activity app, and the activity app's registry, whose "charge" blocks
// until release is closed.
func crossAppRegistries(t *testing.T, actApp string, release <-chan struct{}, ran *atomic.Int32) (*dworkflow.Registry, *dworkflow.Registry) {
	t.Helper()
	orch := dworkflow.NewRegistry()
	require.NoError(t, orch.AddWorkflowN("enc-crossapp", func(ctx *dworkflow.WorkflowContext) (any, error) {
		var in, out string
		if err := ctx.GetInput(&in); err != nil {
			return nil, err
		}
		err := ctx.CallActivity("charge", dworkflow.WithActivityInput(in), dworkflow.WithActivityAppID(actApp)).Await(&out)
		return out, err
	}))
	act := dworkflow.NewRegistry()
	require.NoError(t, act.AddActivityN("charge", func(ctx dworkflow.ActivityContext) (any, error) {
		ran.Add(1)
		var in string
		if err := ctx.GetInput(&in); err != nil {
			return nil, err
		}
		<-release
		return in, nil
	}))
	return orch, act
}

// crossapp runs an activity on another app sharing the key encryption key.
// The activity app seals its reminder with the workflow instance's key.
type crossapp struct {
	*fworkflow.Cluster
	orch, act *daprd.Daprd
}

func (c *crossapp) Setup(t *testing.T) []framework.Option {
	c.Cluster = fworkflow.NewCluster(t)
	kekDir := fworkflow.NewKEKDir(t, "kek.json")
	c.orch = c.Daprd(t, append(fworkflow.EncryptionOptions(t, component, kekDir, "kek.json", false), daprd.WithAppID("enc-orch"))...)
	c.act = c.Daprd(t, append(fworkflow.EncryptionOptions(t, component, kekDir, "kek.json", false), daprd.WithAppID("enc-act"))...)
	return c.Options(c.orch, c.act)
}

func (c *crossapp) Run(t *testing.T, ctx context.Context) {
	release := make(chan struct{})
	var ran atomic.Int32
	orchReg, actReg := crossAppRegistries(t, c.act.AppID(), release, &ran)
	cl := fworkflow.StartWorker(t, ctx, c.orch, orchReg)
	fworkflow.StartWorker(t, ctx, c.act, actReg)

	id, err := cl.ScheduleWorkflow(ctx, "enc-crossapp", dworkflow.WithInput(secret))
	require.NoError(t, err)

	require.EventuallyWithT(t, func(col *assert.CollectT) {
		var found bool
		for _, kv := range c.Scheduler.EtcdJobs(t, ctx) {
			if strings.Contains(string(kv.Key), "run-activity") {
				found = true
				assert.NotContains(col, string(kv.Value), secret)
			}
		}
		assert.True(col, found, "run-activity reminder not created")
	}, 20*time.Second, 50*time.Millisecond)
	close(release)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id, dworkflow.WithFetchPayloads(true))
	require.NoError(t, err)
	require.Equal(t, dworkflow.StatusCompleted, meta.RuntimeStatus)
	assert.Contains(t, meta.Output.GetValue(), secret)
	fworkflow.RequireEncrypted(t, ctx, c.DB, c.Scheduler, id, secret)
}

// crossappmismatch runs an activity on an app holding a different key
// encryption key under the same name. The activity app cannot open the
// workflow's key, so the activity never runs rather than storing anything
// unencrypted.
type crossappmismatch struct {
	*fworkflow.Cluster
	orch, act *daprd.Daprd
	logline   *logline.LogLine
}

func (c *crossappmismatch) Setup(t *testing.T) []framework.Option {
	c.Cluster = fworkflow.NewCluster(t)
	c.logline = logline.New(t, logline.WithStdoutLineContains(
		"failed to open the dispatching workflow's payload encryption key",
	))
	c.orch = c.Daprd(t, append(fworkflow.EncryptionOptions(t, component, fworkflow.NewKEKDir(t, "kek.json"), "kek.json", false),
		daprd.WithAppID("enc-orch-mm"), daprd.WithLogLineStdout(c.logline))...)
	c.act = c.Daprd(t, append(fworkflow.EncryptionOptions(t, component, fworkflow.NewKEKDir(t, "kek.json"), "kek.json", false),
		daprd.WithAppID("enc-act-mm"))...)
	return append([]framework.Option{framework.WithProcesses(c.logline)}, c.Options(c.orch, c.act)...)
}

func (c *crossappmismatch) Run(t *testing.T, ctx context.Context) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	var ran atomic.Int32
	orchReg, actReg := crossAppRegistries(t, c.act.AppID(), release, &ran)
	cl := fworkflow.StartWorker(t, ctx, c.orch, orchReg)
	fworkflow.StartWorker(t, ctx, c.act, actReg)

	id, err := cl.ScheduleWorkflow(ctx, "enc-crossapp", dworkflow.WithInput(secret))
	require.NoError(t, err)

	c.logline.EventuallyFoundAll(t)
	assert.Never(t, func() bool { return ran.Load() > 0 }, 3*time.Second, 100*time.Millisecond)
	meta, err := cl.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, dworkflow.StatusRunning, meta.RuntimeStatus)
	for _, kv := range c.Scheduler.EtcdJobs(t, ctx) {
		assert.NotContains(t, string(kv.Value), secret, "scheduler job %s holds the plaintext", kv.Key)
	}
}
