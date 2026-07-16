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

package workflow

import (
	"context"

	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/activity"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/executor"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/retentioner"
)

type Options struct {
	Orchestrator orchestrator.Options
	Activity     activity.Options
	Retentioner  retentioner.Options
	Executor     *executor.Options

	WorkflowActorType  string
	ActivityActorType  string
	RetentionActorType string
	ExecutorActorType  string
}

func Factories(ctx context.Context, opts Options) ([]table.ActorTypeFactory, error) {
	orchFactory, err := orchestrator.New(ctx, opts.Orchestrator)
	if err != nil {
		return nil, err
	}

	activityFactory, err := activity.New(ctx, opts.Activity)
	if err != nil {
		return nil, err
	}

	retentionerFactory, err := retentioner.New(ctx, opts.Retentioner)
	if err != nil {
		return nil, err
	}

	factories := []table.ActorTypeFactory{
		{
			Factory: orchFactory,
			Type:    opts.WorkflowActorType,
		},
		{
			Factory: activityFactory,
			Type:    opts.ActivityActorType,
		},
		{
			Factory: retentionerFactory,
			Type:    opts.RetentionActorType,
		},
	}

	if opts.Executor != nil {
		executorFactory, err := executor.New(ctx, *opts.Executor)
		if err != nil {
			return nil, err
		}
		factories = append(factories, table.ActorTypeFactory{
			Factory: executorFactory,
			Type:    opts.ExecutorActorType,
		})
	}

	return factories, nil
}
