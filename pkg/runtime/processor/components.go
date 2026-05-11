/*
Copyright 2023 The Dapr Authors
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

package processor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/processor/loops"
)

// AddPendingComponent enqueues a component init and returns a buffered chan
// that receives exactly one error (nil on success). Returns nil if the
// processor is shut down.
func (p *Processor) AddPendingComponent(ctx context.Context, comp compapi.Component) <-chan error {
	if p.closed.Load() {
		return nil
	}
	res := make(chan error, 1)
	p.rootLoop.Loop().Enqueue(&loops.Init{Component: comp, Result: res})
	return res
}

// Init synchronously initialises a component. If Process is running, the
// init is routed through the loop hierarchy and waits for the result;
// otherwise the init runs inline. Tests that drive the processor without
// calling Process rely on the inline fallback.
func (p *Processor) Init(ctx context.Context, comp compapi.Component) error {
	if !p.running.Load() {
		return p.initInline(ctx, comp)
	}
	res := p.AddPendingComponent(ctx, comp)
	if res == nil {
		return errors.New("processor is shut down")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-res:
		return err
	}
}

// Close synchronously closes a component. If Process is running, the close is
// routed through the loop; otherwise it runs inline.
func (p *Processor) Close(comp compapi.Component) error {
	if !p.running.Load() {
		return p.closeInline(comp)
	}
	if p.closed.Load() {
		return p.closeInline(comp)
	}
	res := make(chan error, 1)
	p.rootLoop.Loop().Enqueue(&loops.Close{Component: comp, Result: res})
	return <-res
}

// initInline is the synchronous, non-loop path used by tests that drive the
// processor without calling Process. It mirrors the legacy
// processComponentAndDependents + init dance.
func (p *Processor) initInline(ctx context.Context, comp compapi.Component) error {
	_, unready := p.secret.ProcessResource(ctx, &comp)
	if unready != "" {
		return nil
	}
	cat := p.category(comp)
	if cat == "" {
		return fmt.Errorf("incorrect type %s", comp.Spec.Type)
	}
	mgr, ok := p.inlineManagers[cat]
	if !ok {
		return fmt.Errorf("unknown component category: %q", cat)
	}
	timeout, err := time.ParseDuration(comp.Spec.InitTimeout)
	if err != nil || timeout <= 0 {
		timeout = 5 * time.Second
	}
	initCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	initErr := p.runInlineInit(initCtx, comp, mgr)
	if errors.Is(initCtx.Err(), context.DeadlineExceeded) && initErr == nil {
		initErr = fmt.Errorf("init timeout for component %s", comp.LogName())
	}
	p.reportInline(ctx, comp, operatorv1.EventType_EVENT_INIT, initErr)
	// Match legacy proc.Init: return the sub-processor's wrapped error
	// directly. The "outer" rterrors.NewInit wrap is only applied on the
	// loop path (AddPendingComponent), not on the synchronous Init path.
	return initErr
}

func (p *Processor) runInlineInit(ctx context.Context, comp compapi.Component, mgr inlineManager) error {
	if err := p.compStore.AddPendingComponentForCommit(comp); err != nil {
		return err
	}
	if err := mgr.Init(p.security.WithSVIDContext(ctx), comp); err != nil {
		if derr := p.compStore.DropPendingComponent(); derr != nil {
			return errors.Join(err, derr)
		}
		return err
	}
	if err := p.compStore.CommitPendingComponent(); err != nil {
		return fmt.Errorf("error committing component: %w", err)
	}
	return nil
}

func (p *Processor) closeInline(comp compapi.Component) error {
	cat := p.category(comp)
	if cat == "" {
		return fmt.Errorf("incorrect type %s", comp.Spec.Type)
	}
	mgr, ok := p.inlineManagers[cat]
	if !ok {
		return fmt.Errorf("unknown component category: %q", cat)
	}
	closeErr := mgr.Close(comp)
	p.compStore.DeleteComponent(comp.Name)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.reportInline(ctx, comp, operatorv1.EventType_EVENT_CLOSE, closeErr)
	return closeErr
}

func (p *Processor) reportInline(ctx context.Context, comp compapi.Component, et operatorv1.EventType, opErr error) {
	if p.reporter == nil {
		return
	}
	cond := operatorv1.ResourceConditionStatus_STATUS_SUCCESS
	var reason, message *string
	if opErr != nil {
		cond = operatorv1.ResourceConditionStatus_STATUS_FAILURE
		r := "ERROR"
		m := opErr.Error()
		reason = &r
		message = &m
	}
	if err := p.reporter(ctx, comp, &operatorv1.ResourceResult{
		ResourceType:        operatorv1.ResourceType_RESOURCE_COMPONENT,
		EventType:           et,
		Name:                comp.GetName(),
		Condition:           cond,
		Reason:              reason,
		Message:             message,
		ObservedGeneration:  comp.GetGeneration(),
		LastTransactionTime: timestamppb.New(time.Now()),
	}); err != nil {
		log.Errorf("error reporting component %s result: %s", et, err)
	}
}
