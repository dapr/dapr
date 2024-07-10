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

package helmtpl

import (
	"fmt"
	"regexp"
	"strings"
)

// Option is a function that configures the process.
type Option func(*options)

// options contains the options for running Placement in integration tests.
type options struct {
	setValues       []string
	setStringValues []string
	setJsonValue    string

	// list of resources to show only
	showOnly []string

	exitCode           *int
	exitErrorMsgRegexp *regexp.Regexp
}

func (o options) merge(other options) options {
	for _, v := range other.setValues {
		o.setValues = append(o.setValues, v)
	}
	for _, v := range other.setStringValues {
		o.setStringValues = append(o.setStringValues, v)
	}
	if other.setJsonValue != "" {
		o.setJsonValue = other.setJsonValue
	}
	for _, v := range other.showOnly {
		o.showOnly = append(o.showOnly, v)
	}
	if other.exitCode != nil {
		o.exitCode = other.exitCode
	}
	if other.exitErrorMsgRegexp != nil {
		o.exitErrorMsgRegexp = other.exitErrorMsgRegexp
	}
	return o
}

func (o options) getHelmArgs() (args []string) {
	for _, v := range o.setValues {
		args = append(args, "--set", v)
	}
	for _, v := range o.setStringValues {
		args = append(args, "--set-string", v)
	}
	if o.setJsonValue != "" {
		args = append(args, "--set-json", o.setJsonValue)
	}
	for _, v := range o.showOnly {
		args = append(args, "--show-only", v)
	}
	return args
}

func WithValues(values ...string) Option {
	return func(o *options) {
		o.setValues = append(o.setValues, values...)
	}
}

func WithGlobalValues(values ...string) Option {
	return func(o *options) {
		for i, v := range values {
			values[i] = "global." + v
		}
		o.setValues = append(o.setValues, values...)
	}
}

func WithStringValues(values ...string) Option {
	return func(o *options) {
		o.setStringValues = values
	}
}

func WithJsonValue(jsonString string) Option {
	return func(o *options) {
		o.setJsonValue = jsonString
	}
}

func WithShowOnlySchedulerSTS() Option {
	return func(o *options) {
		o.showOnly = append(o.showOnly, "charts/dapr_scheduler/templates/dapr_scheduler_statefulset.yaml")
	}
}

func WithShowOnlySentryDeploy() Option {
	return func(o *options) {
		o.showOnly = append(o.showOnly, "charts/dapr_sentry/templates/dapr_sentry_deployment.yaml")
	}
}

func WithShowOnlyPlacementSTS() Option {
	return func(o *options) {
		o.showOnly = append(o.showOnly, "charts/dapr_placement/templates/dapr_placement_statefulset.yaml")
	}
}

func WithShowOnlyOperatorDeploy() Option {
	return func(o *options) {
		o.showOnly = append(o.showOnly, "charts/dapr_operator/templates/dapr_operator_deployment.yaml")
	}
}

func WithShowOnlySidecarInjectorDeploy() Option {
	return func(o *options) {
		o.showOnly = append(o.showOnly, "charts/dapr_sidecar_injector/templates/dapr_sidecar_injector_deployment.yaml")
	}
}

// WithShowOnly adds a specific template yaml file at a specific chart to the list of resources to show only
func WithShowOnly(chart, tplYamlFileName string) Option {
	return func(o *options) {
		if !strings.HasSuffix(tplYamlFileName, ".yaml") {
			tplYamlFileName += ".yaml"
		}
		o.showOnly = append(o.showOnly, fmt.Sprintf("charts/%s/templates/%s", chart, tplYamlFileName))
	}
}

func WithExitCode(code int) Option {
	return func(o *options) {
		o.exitCode = &code
	}
}

func WithExitErrorMsgRegex(re string) Option {
	return func(o *options) {
		o.exitErrorMsgRegexp = regexp.MustCompile(re)
	}
}
