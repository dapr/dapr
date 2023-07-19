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

package patcher

import (
	"encoding/json"

	jsonpatch "github.com/evanphx/json-patch/v5"

	"github.com/dapr/kit/ptr"
)

const (
	// Path for patching containers.
	PatchPathContainers = "/spec/containers"
	// Path for patching volumes.
	PatchPathVolumes = "/spec/volumes"
)

// NewPatchOperation returns a jsonpatch.Operation with the provided properties.
// This patch represents a discrete change to be applied to a Kubernetes resource.
func NewPatchOperation(op string, path string, value any) jsonpatch.Operation {
	patchOp := jsonpatch.Operation{
		"op":   ptr.Of(json.RawMessage(`"` + op + `"`)),
		"path": ptr.Of(json.RawMessage(`"` + path + `"`)),
	}

	if value != nil {
		val, _ := json.Marshal(value)
		if len(val) > 0 && string(val) != "null" {
			patchOp["value"] = ptr.Of[json.RawMessage](val)
		}
	}

	return patchOp
}
