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

// Package patcher contains utilities to patch a Pod to inject the Dapr sidecar container.
package patcher

import (
	"encoding/json"
	"fmt"

	jsonpatch "github.com/evanphx/json-patch/v5"
	corev1 "k8s.io/api/core/v1"

	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.injector")

// PatchPod applies a jsonpatch.Patch to a Pod and returns the modified object.
func PatchPod(pod *corev1.Pod, patch jsonpatch.Patch) (*corev1.Pod, error) {
	// Apply the patch
	podJSON, err := json.Marshal(pod)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal pod to JSON: %w", err)
	}
	newJSON, err := patch.Apply(podJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to apply patch: %w", err)
	}

	// Get the Pod object
	newPod := &corev1.Pod{}
	err = json.Unmarshal(newJSON, newPod)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON into a Pod: %w", err)
	}

	return newPod, nil
}
