/*
Copyright 2025 The Dapr Authors
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

package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/dapr/durabletask-go/backend"
)

func (o *orchestrator) createTimers(ctx context.Context, es []*backend.HistoryEvent, generation uint64) error {
	errs := make([]error, len(es))

	var wg sync.WaitGroup
	wg.Add(len(es))
	for i, e := range es {
		go func(i int, e *backend.HistoryEvent) {
			defer wg.Done()

			if err := o.createTimer(ctx, e, generation); err != nil {
				errs[i] = err
			}
		}(i, e)
	}

	wg.Wait()

	return errors.Join(errs...)
}

func (o *orchestrator) createTimer(ctx context.Context, e *backend.HistoryEvent, generation uint64) error {
	ts := e.GetTimerFired()
	if ts == nil {
		return errors.New("invalid timer object for creating a timer reminder")
	}

	delay := time.Until(e.GetTimerFired().GetFireAt().AsTime())
	if delay < 0 {
		delay = 0
	}

	reminderPrefix := "timer-" + strconv.Itoa(int(e.GetTimerFired().GetTimerId()))

	data := &backend.DurableTimer{TimerEvent: e, Generation: generation}

	log.Debugf("Workflow actor '%s': creating reminder '%s' for the durable timer, duetime=%s", o.actorID, reminderPrefix, delay)

	if _, err := o.createReminder(ctx, reminderPrefix, data, delay); err != nil {
		return fmt.Errorf("actor '%s' failed to create reminder for timer: %w", o.actorID, err)
	}

	return nil
}
