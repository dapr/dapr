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

package instance

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/processor/loops"
	"github.com/dapr/dapr/pkg/runtime/registry"
	"github.com/dapr/dapr/pkg/security"
)

var log = logger.NewLogger("dapr.runtime.processor.loops.instance")

// Manager performs Init/Close on a single named component. Per-category
// sub-processors (binding, pubsub, state, secret, ...) satisfy this interface.
type Manager interface {
	Init(ctx context.Context, comp compapi.Component) error
	Close(comp compapi.Component) error
}

// PostInit is an optional capability for managers that need to run additional
// per-instance work after Init succeeds (e.g. bindings starting their input
// pipe when the category is reading). The Instance dispatches StartInput /
// StopInput events to managers implementing this interface.
type PostInit interface {
	StartInput(ctx context.Context, comp compapi.Component) error
	StopInput(comp compapi.Component)
}

type Options struct {
	Manager   Manager
	CompStore *compstore.ComponentStore
	Reporter  registry.Reporter
	Security  security.Handler
	// AlsoStartInput is set when Init for this category should also start the
	// input pipe (bindings: true when readingBindings is set when Init runs).
	AlsoStartInput bool
}

// Instance is the per-named-component loop handler.
type Instance struct {
	manager   Manager
	compStore *compstore.ComponentStore
	reporter  registry.Reporter
	security  security.Handler

	loop loop.Interface[loops.EventInstance]

	// Tracks the last component spec we successfully initialised, used to
	// build Close requests that arrive without a matching Init context.
	lastComp *compapi.Component

	// alsoStartInput indicates that successful Init should be immediately
	// followed by a StartInput.
	alsoStartInput bool
}

func New(opts Options) *Instance {
	i := &Instance{
		manager:        opts.Manager,
		compStore:      opts.CompStore,
		reporter:       opts.Reporter,
		security:       opts.Security,
		alsoStartInput: opts.AlsoStartInput,
	}
	i.loop = loops.InstanceFactory.NewLoop(i)
	return i
}

// Loop returns the underlying loop interface. Callers enqueue instance events
// through it.
func (i *Instance) Loop() loop.Interface[loops.EventInstance] { return i.loop }

// Handle is the loop handler.
func (i *Instance) Handle(ctx context.Context, e loops.EventInstance) error {
	switch ev := e.(type) {
	case *loops.Init:
		i.handleInit(ctx, ev)
	case *loops.Close:
		i.handleClose(ev)
	case *loops.StartInput:
		i.handleStartInput(ctx, ev)
	case *loops.StopInput:
		i.handleStopInput(ev)
	case *loops.Shutdown:
		// Components are closed by an explicit Close event from the caller
		// (see processor.Close and the runtime shutdown path). The Shutdown
		// sentinel exits the loop without performing component close.
	default:
		log.Errorf("instance loop: unknown event type %T", ev)
	}
	// Never return an error from Handle: a loop that errors stops draining and
	// the parent would block on its Close. Errors are reported to callers via
	// per-event Result channels.
	return nil
}

func (i *Instance) handleInit(ctx context.Context, ev *loops.Init) {
	comp := ev.Component
	initCtx := ctx
	if ev.Timeout > 0 {
		var cancel context.CancelFunc
		initCtx, cancel = context.WithTimeout(ctx, ev.Timeout)
		defer cancel()
	}
	initerr := i.runInit(initCtx, comp)
	// A timed-out init whose manager returned nil must still surface as an
	// error, matching the legacy synchronous Init and the inline path. Checked
	// against the init context the manager actually used.
	if errors.Is(initCtx.Err(), context.DeadlineExceeded) && initerr == nil {
		initerr = fmt.Errorf("init timeout for component %s", comp.LogName())
	}
	if initerr == nil {
		i.lastComp = &comp
		if i.alsoStartInput {
			if post, ok := i.manager.(PostInit); ok {
				if err := post.StartInput(ctx, comp); err != nil {
					log.Errorf("error starting input for %s: %s", comp.LogName(), err)
				}
			}
		}
	}
	i.report(ctx, comp, operatorv1.EventType_EVENT_INIT, initerr)
	sendResult(ev.Result, initerr)
}

func (i *Instance) runInit(ctx context.Context, comp compapi.Component) error {
	if err := i.compStore.AddPendingComponentForCommit(comp); err != nil {
		return err
	}
	if err := i.manager.Init(i.security.WithSVIDContext(ctx), comp); err != nil {
		if derr := i.compStore.DropPendingComponent(); derr != nil {
			return errors.Join(err, derr)
		}
		return err
	}
	if err := i.compStore.CommitPendingComponent(); err != nil {
		return fmt.Errorf("error committing component: %w", err)
	}
	return nil
}

func (i *Instance) handleClose(ev *loops.Close) {
	comp := ev.Component
	closeErr := i.manager.Close(comp)
	i.compStore.DeleteComponent(comp.Name)
	if closeErr == nil {
		i.lastComp = nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	i.report(ctx, comp, operatorv1.EventType_EVENT_CLOSE, closeErr)
	sendResult(ev.Result, closeErr)
}

func (i *Instance) handleStartInput(ctx context.Context, ev *loops.StartInput) {
	post, ok := i.manager.(PostInit)
	if !ok {
		sendResult(ev.Result, nil)
		return
	}
	if i.lastComp == nil {
		sendResult(ev.Result, nil)
		return
	}
	err := post.StartInput(ctx, *i.lastComp)
	sendResult(ev.Result, err)
}

func (i *Instance) handleStopInput(ev *loops.StopInput) {
	if post, ok := i.manager.(PostInit); ok && i.lastComp != nil {
		post.StopInput(*i.lastComp)
	}
	if ev.Done != nil {
		close(ev.Done)
	}
}

// report mirrors the legacy Processor.Init/Close reporter wrapping.
func (i *Instance) report(ctx context.Context, comp compapi.Component, et operatorv1.EventType, opErr error) {
	if i.reporter == nil {
		return
	}
	condition := operatorv1.ResourceConditionStatus_STATUS_SUCCESS
	var reason, message *string
	if opErr != nil {
		condition = operatorv1.ResourceConditionStatus_STATUS_FAILURE
		r := "ERROR"
		m := opErr.Error()
		reason = &r
		message = &m
	}
	if err := i.reporter(ctx, comp, &operatorv1.ResourceResult{
		ResourceType:        operatorv1.ResourceType_RESOURCE_COMPONENT,
		EventType:           et,
		Name:                comp.GetName(),
		Condition:           condition,
		Reason:              reason,
		Message:             message,
		ObservedGeneration:  comp.GetGeneration(),
		LastTransactionTime: timestamppb.New(time.Now()),
	}); err != nil {
		log.Errorf("error reporting component %s result: %s", et, err)
	}
}

// WrapInitFailure formats Init errors in the legacy shape so callers (and
// tests) see the same string they did before the refactor.
func WrapInitFailure(comp compapi.Component, err error) error {
	if err == nil {
		return nil
	}
	diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.Name)
	return rterrors.NewInit(rterrors.InitComponentFailure, comp.LogName(), err)
}

func sendResult(ch chan<- error, err error) {
	if ch == nil {
		return
	}
	select {
	case ch <- err:
	default:
	}
}
