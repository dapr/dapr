//go:build allcomponents

/*
Copyright 2021 The Dapr Authors
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

package components

import (
	wfbe "github.com/dapr/components-contrib/wfbackend"
	backendLoader "github.com/dapr/dapr/pkg/components/workflowBackend"
)

func init() {
	backendLoader.DefaultRegistry.RegisterComponent(wfbe.NewWorkflowBackendComp, "actor")
}
