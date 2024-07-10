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

package helm

import (
	"fmt"
	"strings"

	"github.com/dapr/dapr/tests/integration/framework/process/exec"
)

// OptionFunc is a function that configures the process.
type OptionFunc func(*options)

// options contains the options for running helm in integration tests.
type options struct {
	execOpts []exec.Option

	setValues       []string
	setStringValues []string
	setJSONValue    string

	// list of resources to show only
	showOnly []string

	// if set we use the local buffer to read/write the stdout
	// Note: we will not be using execOpts stdout if passed as an exec OptionFunc
	useLocalBuffForStdout bool
}

func WithExecOptions(execOptions ...exec.Option) OptionFunc {
	return func(o *options) {
		o.execOpts = append(o.execOpts, execOptions...)
	}
}

func WithLocalBuffForStdout() OptionFunc {
	return func(o *options) {
		o.useLocalBuffForStdout = true
	}
}

func WithValues(values ...string) OptionFunc {
	return func(o *options) {
		o.setValues = append(o.setValues, values...)
	}
}

func WithGlobalValues(values ...string) OptionFunc {
	return func(o *options) {
		for _, v := range values {
			o.setValues = append(o.setValues, "global."+v)
		}

	}
}

func WithStringValues(values ...string) OptionFunc {
	return func(o *options) {
		o.setStringValues = values
	}
}

func WithJSONValue(jsonString string) OptionFunc {
	return func(o *options) {
		o.setJSONValue = jsonString
	}
}

func WithShowOnlySchedulerSTS() OptionFunc {
	return func(o *options) {
		o.showOnly = append(o.showOnly, "charts/dapr_scheduler/templates/dapr_scheduler_statefulset.yaml")
	}
}

func WithShowOnlySentryDeploy() OptionFunc {
	return func(o *options) {
		o.showOnly = append(o.showOnly, "charts/dapr_sentry/templates/dapr_sentry_deployment.yaml")
	}
}

func WithShowOnlyPlacementSTS() OptionFunc {
	return func(o *options) {
		o.showOnly = append(o.showOnly, "charts/dapr_placement/templates/dapr_placement_statefulset.yaml")
	}
}

func WithShowOnlyOperatorDeploy() OptionFunc {
	return func(o *options) {
		o.showOnly = append(o.showOnly, "charts/dapr_operator/templates/dapr_operator_deployment.yaml")
	}
}

func WithShowOnlySidecarInjectorDeploy() OptionFunc {
	return func(o *options) {
		o.showOnly = append(o.showOnly, "charts/dapr_sidecar_injector/templates/dapr_sidecar_injector_deployment.yaml")
	}
}

// WithShowOnly adds a specific template yaml file at a specific chart to the list of resources to show only
func WithShowOnly(chart, tplYamlFileName string) OptionFunc {
	return func(o *options) {
		if !strings.HasSuffix(tplYamlFileName, ".yaml") {
			tplYamlFileName += ".yaml"
		}
		o.showOnly = append(o.showOnly, fmt.Sprintf("charts/%s/templates/%s", chart, tplYamlFileName))
	}
}
