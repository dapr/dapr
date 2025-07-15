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

	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/activity"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator"
)

func Factories(ctx context.Context, o orchestrator.Options, a activity.Options) (targets.Factory, targets.Factory, error) {
	orchFactory, err := orchestrator.Factory(ctx, o)
	if err != nil {
		return nil, nil, err
	}

	activityFactory, err := activity.Factory(ctx, a)
	if err != nil {
		return nil, nil, err
	}

	return orchFactory, activityFactory, nil
}
