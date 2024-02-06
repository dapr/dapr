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

package diagnostics

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConvertPathToMethodName(t *testing.T) {
	convertTests := []struct {
		in  string
		out string
	}{
		{"/v1/state/statestore/key", "/v1/state/statestore"},
		{"/v1/state/statestore", "/v1/state/statestore"},
		{"/v1/secrets/keyvault/name", "/v1/secrets/keyvault"},
		{"/v1/publish/topic", "/v1/publish/topic"},
		{"/v1/bindings/kafka", "/v1/bindings/kafka"},
		{"/healthz", "/healthz"},
		{"/v1/actors/DemoActor/1/state/key", "/v1/actors/DemoActor/{id}/state"},
		{"/v1/actors/DemoActor/1/reminder/name", "/v1/actors/DemoActor/{id}/reminder"},
		{"/v1/actors/DemoActor/1/timer/name", "/v1/actors/DemoActor/{id}/timer"},
		{"/v1/actors/DemoActor/1/timer/name?query=string", "/v1/actors/DemoActor/{id}/timer"},
		{"v1/actors/DemoActor/1/timer/name", "/v1/actors/DemoActor/{id}/timer"},
		{"actors/DemoActor/1/method/method1", "actors/DemoActor/{id}/method/method1"},
		{"actors/DemoActor/1/method/timer/timer1", "actors/DemoActor/{id}/method/timer/timer1"},
		{"actors/DemoActor/1/method/remind/reminder1", "actors/DemoActor/{id}/method/remind/reminder1"},
		{"/v1.0-alpha1/workflows/workflowComponentName/mywf/start?instanceID=1234", "/v1.0-alpha1/workflows/workflowComponentName/mywf/start"},
		{"/v1.0-alpha1/workflows/workflowComponentName/mywf/start", "/v1.0-alpha1/workflows/workflowComponentName/mywf/start"},
		{"/v1.0-alpha1/workflows/workflowComponentName/1234/start/value1/value2", "/v1.0-alpha1/workflows/workflowComponentName/{instanceId}/start"},
		{"/v1.0-alpha1/workflows/workflowComponentName/1234/terminate", "/v1.0-alpha1/workflows/workflowComponentName/{instanceId}/terminate"},
		{"/v1.0-alpha1/workflows/workflowComponentName/1234/terminate/value1/value2", "/v1.0-alpha1/workflows/workflowComponentName/{instanceId}/terminate"},
		{"/v1.0-alpha1/workflows/workflowComponentName/1234/raiseEvent/foobaz", "/v1.0-alpha1/workflows/workflowComponentName/{instanceId}/raiseEvent/{eventName}"},
		{"/v1.0-alpha1/workflows/workflowComponentName/1234/pause", "/v1.0-alpha1/workflows/workflowComponentName/{instanceId}/pause"},
		{"/v1.0-alpha1/workflows/workflowComponentName/1234/resume", "/v1.0-alpha1/workflows/workflowComponentName/{instanceId}/resume"},
		{"/v1.0-alpha1/workflows/workflowComponentName/1234/purge", "/v1.0-alpha1/workflows/workflowComponentName/{instanceId}/purge"},
		{"/v1.0-alpha1/workflows/workflowComponentName/1234", "/v1.0-alpha1/workflows/workflowComponentName/{instanceId}"},
		{"/v1.0-alpha1/workflows/workflowComponentName", "/v1.0-alpha1/workflows/workflowComponentName"},
		{"/v1.0-alpha1/workflows", "/v1.0-alpha1/workflows"},
		{"", ""},
	}

	testHTTP := newHTTPMetrics()
	for _, tt := range convertTests {
		t.Run(tt.in, func(t *testing.T) {
			lowCardinalityName := testHTTP.convertPathToMetricLabel(tt.in)
			assert.Equal(t, tt.out, lowCardinalityName)
		})
	}
}
