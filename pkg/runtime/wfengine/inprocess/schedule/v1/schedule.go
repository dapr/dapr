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

// Package schedule runs workflow schedules: each schedule is one instance of
// an in-process workflow that waits on a durable timer until the next time
// the schedule matches, then starts the user's workflow as a detached
// workflow.
package schedule

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
	"github.com/dapr/kit/cron"
)

const (
	// WorkflowName is the in-process workflow that runs a schedule. Its
	// prefix routes it to the in-process executor.
	WorkflowName = "dapr.internal.schedule"

	// InstanceIDPrefix starts the instance ID of every schedule's workflow,
	// followed by the schedule name.
	InstanceIDPrefix = "dapr-internal-schedule-"

	// triggerEvent is raised on a schedule's workflow to start a run now.
	triggerEvent = "dapr.internal.schedule.trigger"

	runTimeFormat       = "20060102T150405Z"
	manualRunTimeFormat = "20060102T150405.000Z"
)

var (
	nameRegexp = regexp.MustCompile(`^[A-Za-z0-9-]{1,30}$`)

	// parser is the one go-etcd-cron uses for the Jobs API.
	parser = cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
)

// Register adds the schedule workflow to the in-process registry. It is
// versioned so that a change to it can be a new version, while running
// schedules replay the one they started with.
func Register(r *task.TaskRegistry) {
	r.UpsertVersionedWorkflowN(WorkflowName, "v1", true, run)
}

// Owner tags the run IDs of the schedules an app owns, so that same-named
// schedules of two apps that target one app do not share run IDs.
func Owner(namespace, appID string) string {
	sum := sha256.Sum256([]byte(namespace + "/" + appID))
	return hex.EncodeToString(sum[:3])
}

// RunInstanceID is the instance ID of the run a schedule starts for time t.
func RunInstanceID(name, owner string, t time.Time) string {
	return name + "_" + owner + "_" + t.UTC().Format(runTimeFormat)
}

// ManualRunInstanceID is the instance ID of a run triggered at time t. It
// has milliseconds, without the dot instance IDs cannot hold.
func ManualRunInstanceID(name, owner string, t time.Time) string {
	return name + "_" + owner + "_" + strings.Replace(t.UTC().Format(manualRunTimeFormat), ".", "", 1) + "_manual"
}

// state is the input of a schedule's workflow. It is carried from one
// generation to the next with ContinueAsNew.
type state struct {
	// Schedule is the protojson of the WorkflowSchedule, with no status.
	Schedule  json.RawMessage `json:"schedule"`
	Owner     string          `json:"owner"`
	CreatedAt time.Time       `json:"createdAt"`
	RunCount  uint32          `json:"runCount,omitempty"`
	// LastScheduled is the last time the schedule matched and a run started
	// for it. The next match is after it, so a timer firing early cannot
	// start a run twice.
	LastScheduled *time.Time `json:"lastScheduled,omitempty"`
	// LastRun and LastInstanceID are the last run, scheduled or triggered.
	LastRun        *time.Time `json:"lastRun,omitempty"`
	LastInstanceID string     `json:"lastInstanceId,omitempty"`
	// Due is the time whose run the next generation starts.
	Due *time.Time `json:"due,omitempty"`
}

type trigger struct {
	InstanceID string `json:"instanceId"`
}

func (s *state) schedule() (*protos.WorkflowSchedule, error) {
	var ws protos.WorkflowSchedule
	if err := protojson.Unmarshal(s.Schedule, &ws); err != nil {
		return nil, fmt.Errorf("invalid workflow schedule state: %w", err)
	}
	return &ws, nil
}

// status is the schedule's status, which its workflow keeps as its custom
// status.
func (s *state) status() *protos.WorkflowScheduleStatus {
	st := &protos.WorkflowScheduleStatus{
		State:     protos.WorkflowScheduleState_WORKFLOW_SCHEDULE_STATE_ACTIVE,
		CreatedAt: timestamppb.New(s.CreatedAt),
		RunCount:  s.RunCount,
	}
	if s.LastRun != nil {
		st.LastRunTime = timestamppb.New(*s.LastRun)
		st.LastRun = &protos.WorkflowScheduleRun{InstanceId: s.LastInstanceID}
	}
	return st
}

// parse parses a schedule and rejects the ones that would fire in a tight
// loop.
func parse(spec string) (cron.Schedule, error) {
	sched, err := parser.Parse(spec)
	if err != nil {
		return nil, fmt.Errorf("invalid schedule %q: %w", spec, err)
	}
	if d, ok := sched.(cron.ConstantDelaySchedule); ok && d.Delay < time.Second {
		return nil, fmt.Errorf("invalid schedule %q: the interval must be at least one second", spec)
	}
	return sched, nil
}

// next is the time of the schedule's next run, and false when it has none
// left. It follows the last time a run started for, as Dapr jobs and actor
// reminders do, so after downtime it is in the past and every missed time
// gets its run, one per timer turn. With skipMissed, the missed times are
// skipped and it is the first time after now.
// ponytail: one catch-up run per timer turn. Starting several in one turn
// would catch up faster, but Catalyst admits a turn's runs all or nothing, so
// it would need per-run admission there first.
func next(ws *protos.WorkflowSchedule, sched cron.Schedule, s *state, now time.Time) (time.Time, bool) {
	if ws.Repeats != nil && s.RunCount >= ws.GetRepeats() {
		return time.Time{}, false
	}

	var t time.Time
	switch {
	case s.LastScheduled != nil:
		if t = sched.Next(*s.LastScheduled); ws.GetSkipMissed() && !t.After(now) {
			t = sched.Next(now)
		}
	case ws.GetDueTime() != nil:
		t = ws.GetDueTime().AsTime()
	default:
		t = sched.Next(now)
	}

	if t.IsZero() || (ws.GetExpirationTime() != nil && t.After(ws.GetExpirationTime().AsTime())) {
		return time.Time{}, false
	}
	return t, true
}

// run is one generation of a schedule's workflow. It starts the run its
// predecessor was due to, then waits for the next time the schedule matches,
// starting a run for every trigger meanwhile, and continues as new. When the
// next time has already passed, as after downtime, the wait ends at once, so
// each missed time still gets its run. A detached start and ContinueAsNew are
// never in the same turn: ContinueAsNew discards the other actions of its
// turn.
func run(ctx *task.WorkflowContext) (any, error) {
	var s state
	if err := ctx.GetInput(&s); err != nil {
		return nil, err
	}
	ws, err := s.schedule()
	if err != nil {
		return nil, err
	}
	sched, err := parse(ws.GetSchedule())
	if err != nil {
		return nil, err
	}

	if s.Due != nil {
		due := *s.Due
		s.Due, s.LastScheduled = nil, &due
		if err := start(ctx, ws, &s, RunInstanceID(ws.GetName(), s.Owner, due), due); err != nil {
			return nil, err
		}
	}

	t, ok := next(ws, sched, &s, ctx.CurrentTimeUtc)
	for ok {
		st := s.status()
		st.NextRunTime = timestamppb.New(t)
		if err := setStatus(ctx, st); err != nil {
			return nil, err
		}

		// A zero timeout would not wait at all.
		var tr trigger
		err := ctx.WaitForSingleEvent(triggerEvent, max(t.Sub(ctx.CurrentTimeUtc), time.Nanosecond)).Await(&tr)
		if errors.Is(err, task.ErrTaskCanceled) {
			s.Due = &t
			ctx.ContinueAsNew(s, task.WithKeepUnprocessedEvents())
			return nil, nil
		}

		// Any other error is a trigger whose payload does not decode, as one
		// raised by hand: it still starts a run, under one of this schedule's
		// run IDs whatever the payload names.
		if err != nil || !strings.HasPrefix(tr.InstanceID, ws.GetName()+"_"+s.Owner+"_") {
			tr.InstanceID = ManualRunInstanceID(ws.GetName(), s.Owner, ctx.CurrentTimeUtc)
		}
		// Triggers in the same millisecond share a run ID, so are one run.
		if tr.InstanceID == s.LastInstanceID {
			continue
		}
		if err := start(ctx, ws, &s, tr.InstanceID, ctx.CurrentTimeUtc); err != nil {
			return nil, err
		}
		if ws.Repeats != nil && s.RunCount >= ws.GetRepeats() {
			ok = false
		}
	}

	st := s.status()
	st.State = protos.WorkflowScheduleState_WORKFLOW_SCHEDULE_STATE_COMPLETED
	return nil, setStatus(ctx, st)
}

// start starts a run of the schedule's workflow as a detached workflow.
func start(ctx *task.WorkflowContext, ws *protos.WorkflowSchedule, s *state, id string, at time.Time) error {
	opts := []task.DetachedWorkflowOptions{task.WithDetachedWorkflowInstanceID(id)}
	if ws.GetInput() != nil {
		opts = append(opts, task.WithRawDetachedWorkflowInput(ws.GetInput()))
	}
	if ws.TargetAppID != nil {
		opts = append(opts, task.WithDetachedWorkflowAppID(ws.GetTargetAppID()))
	}
	if ws.TargetAppNamespace != nil {
		opts = append(opts, task.WithDetachedWorkflowAppNamespace(ws.GetTargetAppNamespace()))
	}
	if _, err := ctx.ScheduleNewDetachedWorkflow(ws.GetWorkflowName(), opts...); err != nil {
		return err
	}
	s.RunCount++
	s.LastRun, s.LastInstanceID = &at, id
	return nil
}

func setStatus(ctx *task.WorkflowContext, st *protos.WorkflowScheduleStatus) error {
	b, err := protojson.Marshal(st)
	if err != nil {
		return err
	}
	ctx.SetCustomStatus(string(b))
	return nil
}
