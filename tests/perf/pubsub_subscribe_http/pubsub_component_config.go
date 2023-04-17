//go:build perf
// +build perf

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

package pubsub_subscribe_http

type PubsubComponentConfig struct {
	Name       string      `yaml:"name"`
	Components []Component `yaml:"components"`
}

type Component struct {
	Name                         string            `yaml:"name"`
	Topic                        string            `yaml:"topic"`
	Route                        string            `yaml:"route"`
	NumHealthChecks              int               `yaml:"numHealthChecks"`
	TestAppName                  string            `yaml:"testAppName"`
	TestLabel                    string            `yaml:"testLabel"`
	SubscribeHTTPThresholdMs     int               `yaml:"subscribeHTTPThresholdMs"`
	BulkSubscribeHTTPThresholdMs int               `yaml:"bulkSubscribeHTTPThresholdMs"`
	ImageName                    string            `yaml:"imageName"`
	Metadata                     map[string]string `yaml:"metadata,omitempty"`
	Operations                   []string          `yaml:"operations"`
}
