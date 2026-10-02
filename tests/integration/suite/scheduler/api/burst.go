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

package api

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(burst))
}

// burst schedules waves of due-now jobs from many clients at once, letting the
// trigger queue drain between waves. Every job in every wave must trigger.
type burst struct {
	scheduler *scheduler.Scheduler
}

func (b *burst) Setup(t *testing.T) []framework.Option {
	b.scheduler = scheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(b.scheduler),
	}
}

func (b *burst) Run(t *testing.T, ctx context.Context) {
	b.scheduler.WaitUntilRunning(t, ctx)

	const (
		apps      = 3
		producers = 4
		perWave   = 1
		waves     = 50
	)

	client := b.scheduler.Client(t, ctx)

	triggered := make(chan string, apps*producers*perWave)
	for a := range apps {
		ch := b.scheduler.WatchJobsSuccess(t, ctx, &schedulerv1.WatchJobsRequestInitial{
			AppId:     "app" + strconv.Itoa(a),
			Namespace: "default",
		})
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case name := <-ch:
					select {
					case triggered <- name:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}

	for wave := range waves {
		expected := make(map[string]struct{}, apps*producers*perWave)
		errs := make(chan error, apps*producers*perWave)
		var wg sync.WaitGroup

		for a := range apps {
			for p := range producers {
				for j := range perWave {
					name := "w" + strconv.Itoa(wave) + "-a" + strconv.Itoa(a) + "-p" + strconv.Itoa(p) + "-j" + strconv.Itoa(j)
					expected[name] = struct{}{}
					wg.Go(func() {
						_, err := client.ScheduleJob(ctx, b.scheduler.JobNowJob(name, "default", "app"+strconv.Itoa(a)))
						errs <- err
					})
				}
			}
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}

		deadline := time.NewTimer(10 * time.Second)
		for len(expected) > 0 {
			select {
			case name := <-triggered:
				delete(expected, name)
			case <-deadline.C:
				require.FailNowf(t, "jobs never triggered", "wave %d: %d jobs not triggered: %v", wave, len(expected), expected)
			}
		}
		deadline.Stop()
	}
}
