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
package wfengine

import (
	"github.com/dapr/components-contrib/workflows" // This will be removed
	"github.com/dapr/kit/logger"
)

func BuiltinBackendFactory() func(logger.Logger) workflows.WorkflowBackend {
	return func(logger logger.Logger) workflows.WorkflowBackend {
		return &workflowBackendComponent{
			logger: logger,
		}
	}
}

type workflowBackendComponent struct {
	logger logger.Logger
}

func (c *workflowBackendComponent) Init(metadata workflows.Metadata) error {
	c.logger.Info("Initializing Dapr workflow component")
	return nil
}
