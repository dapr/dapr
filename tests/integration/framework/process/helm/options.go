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
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework/process/exec"
)

// OptionFunc is a function that configures the process.
type OptionFunc func(*options)

// options contains the options for running helm in integration tests.
type options struct {
	execOpts []exec.Option

	stdout io.WriteCloser

	setValues       []string
	setStringValues []string
	setJSONValue    *string

	// list of resources to show only
	showOnly []string

	namespace *string
}

func WithExit1() OptionFunc {
	return func(o *options) {
		o.execOpts = append(o.execOpts,
			exec.WithExitCode(1),
			exec.WithRunError(func(t *testing.T, err error) {
				//nolint:testifylint
				assert.ErrorContains(t, err, "exit status 1")
			}),
		)
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
		o.setJSONValue = &jsonString
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

func WithNamespace(namespace string) OptionFunc {
	return func(o *options) {
		o.namespace = &namespace
	}
}

func WithStdout(stdout io.WriteCloser) OptionFunc {
	return func(o *options) {
		o.execOpts = append(o.execOpts, exec.WithStdout(stdout))
	}
}

func WithStderr(stderr io.WriteCloser) OptionFunc {
	return func(o *options) {
		o.execOpts = append(o.execOpts, exec.WithStderr(stderr))
	}
}
