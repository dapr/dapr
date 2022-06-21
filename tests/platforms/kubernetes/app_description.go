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

package kubernetes

import (
	"encoding/json"

	apiv1 "k8s.io/api/core/v1"
)

// AppDescription holds the deployment information of test app.
type AppDescription struct {
	AppName            string            `json:",omitempty"`
	AppPort            int               `json:",omitempty"`
	AppProtocol        string            `json:",omitempty"`
	AppEnv             map[string]string `json:",omitempty"`
	DaprEnabled        bool              `json:",omitempty"`
	ImageName          string            `json:",omitempty"`
	ImageSecret        string            `json:",omitempty"`
	RegistryName       string            `json:",omitempty"`
	Replicas           int32             `json:",omitempty"`
	IngressEnabled     bool              `json:",omitempty"`
	MetricsEnabled     bool              `json:",omitempty"` // This controls the setting for the dapr.io/enable-metrics annotation
	MetricsPort        string            `json:",omitempty"`
	Config             string            `json:",omitempty"`
	AppCPULimit        string            `json:",omitempty"`
	AppCPURequest      string            `json:",omitempty"`
	AppMemoryLimit     string            `json:",omitempty"`
	AppMemoryRequest   string            `json:",omitempty"`
	DaprCPULimit       string            `json:",omitempty"`
	DaprCPURequest     string            `json:",omitempty"`
	DaprMemoryLimit    string            `json:",omitempty"`
	DaprMemoryRequest  string            `json:",omitempty"`
	Namespace          *string           `json:",omitempty"`
	IsJob              bool              `json:",omitempty"`
	SecretStoreDisable bool              `json:",omitempty"`
	DaprVolumeMounts   string            `json:",omitempty"`
	Labels             map[string]string `json:",omitempty"` // Adds custom labels to pods
	PodAffinityLabels  map[string]string `json:",omitempty"` // If set, adds a podAffinity rule matching those labels
	Volumes            []apiv1.Volume    `json:",omitempty"`
	InitContainers     []apiv1.Container `json:",omitempty"`
	PlacementAddresses []string          `json:",omitempty"`
}

func (a AppDescription) String() string {
	// AppDescription objects can contain credentials in ImageSecret which should not be exposed in logs.
	// This method overrides the default stringifier to use the custom JSON stringifier which hides ImageSecret
	j, _ := json.Marshal(a)
	return string(j)
}

func (a AppDescription) MarshalJSON() ([]byte, error) {
	imageSecret := a.ImageSecret
	if imageSecret != "" {
		imageSecret = "***"
	}
	type Alias AppDescription
	return json.Marshal(&struct {
		ImageSecret string `json:",omitempty"`
		*Alias
	}{
		Alias:       (*Alias)(&a),
		ImageSecret: imageSecret,
	})
}
