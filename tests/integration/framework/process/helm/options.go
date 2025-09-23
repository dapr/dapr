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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/binary"
)

// OptionFunc is a function that configures the process.
type OptionFunc func(*options)

// options contains the options for running helm in integration tests.
type options struct {
	setValues []string

	// list of resources to show only
	showOnly []string

	namespace *string
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

func WithShowOnlySchedulerSTS() OptionFunc {
	return func(o *options) {
		o.showOnly = append(o.showOnly, "charts/dapr_scheduler/templates/dapr_scheduler_statefulset.yaml")
	}
}

func WithShowOnlySentryDeployment() OptionFunc {
	return func(o *options) {
		o.showOnly = append(o.showOnly, "charts/dapr_sentry/templates/dapr_sentry_deployment.yaml")
	}
}

func WithShowOnlyServices(t *testing.T) OptionFunc {
	return func(o *options) {
		require.NoError(t, filepath.Walk(
			filepath.Join(binary.RootDir(t), "charts/dapr/charts"),
			func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if info.IsDir() {
					return nil
				}

				if strings.HasSuffix(path, "service.yaml") {
					chartPath := filepath.Join(binary.RootDir(t), "/charts/dapr")
					relativePath, err := filepath.Rel(chartPath, path)
					if err != nil {
						return err
					}
					o.showOnly = append(o.showOnly, relativePath)
				}
				return nil
			},
		))
	}
}

// WithShowOnly adds a specific template yaml file at a specific chart to the list of resources to show only
func WithShowOnly(chart, tplYamlFileName string) OptionFunc {
	return func(o *options) {
		if !strings.HasSuffix(tplYamlFileName, ".yaml") {
			tplYamlFileName += ".yaml"
		}
		o.showOnly = append(o.showOnly, fmt.Sprintf("%s/templates/%s", chart, tplYamlFileName))
	}
}

func WithNamespace(namespace string) OptionFunc {
	return func(o *options) {
		o.namespace = &namespace
	}
}
