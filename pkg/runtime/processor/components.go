/*
Copyright 2024 The Dapr Authors
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
	"strings"
	"time"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/components"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
)

// Init initializes a component of a category.
func (p *Processor) Init(ctx context.Context, comp componentsapi.Component) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	m, err := p.managerFromComp(comp)
	if err != nil {
		return err
	}

	if err := p.compStore.AddPendingComponentForCommit(comp); err != nil {
		return err
	}

	if err := m.Init(p.security.WithSVIDContext(ctx), comp); err != nil {
		return errors.Join(err, p.compStore.DropPendingComponent())
	}

	return p.compStore.CommitPendingComponent()
}

// Close closes the component.
func (p *Processor) Close(comp componentsapi.Component) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	m, err := p.managerFromComp(comp)
	if err != nil {
		return err
	}

	if err := m.Close(comp); err != nil {
		return err
	}

	p.compStore.DeleteComponent(comp.Name)

	return nil
}

func (p *Processor) AddPendingComponent(ctx context.Context, comp componentsapi.Component) bool {
	p.chlock.RLock()
	defer p.chlock.RUnlock()

	if p.shutdown.Load() {
		return false
	}

	p.pendingComponentsWaiting.Add(1)
	select {
	case <-ctx.Done():
		p.pendingComponentsWaiting.Done()
		return false
	case <-p.closedCh:
		p.pendingComponentsWaiting.Done()
		return false
	case p.pendingComponents <- comp:
		return true
	}
}

func (p *Processor) processComponents(ctx context.Context) error {
	process := func(comp componentsapi.Component) error {
		if comp.Name == "" {
			return nil
		}

		err := p.processComponentAndDependents(ctx, comp)
		if err != nil {
			err = fmt.Errorf("process component %s error: %s", comp.Name, err)
			if !comp.Spec.IgnoreErrors {
				log.Warnf("Error processing component, daprd will exit gracefully: %s", err)
				return err
			}
			log.Errorf("Ignoring error processing component: %s", err)
		}
		return nil
	}

	for comp := range p.pendingComponents {
		err := process(comp)
		p.pendingComponentsWaiting.Done()
		if err != nil {
			return err
		}
	}

	return nil
}

// WaitForEmptyComponentQueue waits for the component queue to be empty.
func (p *Processor) WaitForEmptyComponentQueue() {
	p.pendingComponentsWaiting.Wait()
}

func (p *Processor) processComponentAndDependents(ctx context.Context, comp componentsapi.Component) error {
	log.Debug("Loading component: " + comp.LogName() + "type: " + comp.Spec.Type)
	res := p.preprocessOneComponent(ctx, &comp)
	if res.unreadyDependency != "" {
		p.pendingComponentDependents[res.unreadyDependency] = append(p.pendingComponentDependents[res.unreadyDependency], comp)
		return nil
	}

	compCategory := p.category(comp)
	if compCategory == "" {
		// the category entered is incorrect, return error
		return fmt.Errorf("incorrect type %s", comp.Spec.Type)
	}

	timeout, err := time.ParseDuration(comp.Spec.InitTimeout)
	if err != nil {
		timeout = defaultComponentInitTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	err = p.Init(ctx, comp)
	// If the context is canceled, we want  to return an init error.
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("init timeout for component %s exceeded after %s", comp.LogName(), timeout.String())
	}
	if err != nil {
		log.Errorf("Failed to init component %s: %s", comp.LogName(), err)
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, comp.LogName(), err)
	}

	log.Info("Component loaded: " + comp.LogName())
	diag.DefaultMonitoring.ComponentLoaded()

	dependency := componentDependency(compCategory, comp.Name)
	if deps, ok := p.pendingComponentDependents[dependency]; ok {
		delete(p.pendingComponentDependents, dependency)
		for _, dependent := range deps {
			if err := p.processComponentAndDependents(ctx, dependent); err != nil {
				return err
			}
		}
	}

	return nil
}

type componentPreprocessRes struct {
	unreadyDependency string
}

func (p *Processor) preprocessOneComponent(ctx context.Context, comp *componentsapi.Component) componentPreprocessRes {
	_, unreadySecretsStore := p.secret.ProcessResource(ctx, comp)
	if unreadySecretsStore != "" {
		return componentPreprocessRes{
			unreadyDependency: componentDependency(components.CategorySecretStore, unreadySecretsStore),
		}
	}
	return componentPreprocessRes{}
}

func (p *Processor) category(comp componentsapi.Component) components.Category {
	for category := range p.managers {
		if strings.HasPrefix(comp.Spec.Type, string(category)+".") {
			return category
		}
	}
	return ""
}

func componentDependency(compCategory components.Category, name string) string {
	return string(compCategory) + ":" + name
}
