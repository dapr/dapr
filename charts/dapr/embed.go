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

// Package daprcrds embeds the Dapr CRD YAML files so the runtime can extract
// validation rules (CEL, minLength, etc.) for standalone mode where there
// is no Kubernetes API server to enforce them.
package daprcrds

import _ "embed"

//go:embed crds/mcpservers.yaml
var MCPServerCRD []byte
