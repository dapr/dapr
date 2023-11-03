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

package httpendpoint

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	httpEndpointV1alpha1 "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/utils"
)

const (
	httpEndpointKind = "HTTPEndpoint"
)

// LoadLocalEndpoint loads endpoint configurations from local folders.
func LoadLocalEndpoint(log logger.Logger, runtimeID string, paths ...string) []*httpEndpointV1alpha1.HTTPEndpoint {
	configs := []*httpEndpointV1alpha1.HTTPEndpoint{}
	for _, path := range paths {
		loaded := loadLocalHTTPEndpointPath(log, runtimeID, path)
		if len(loaded) > 0 {
			configs = append(configs, loaded...)
		}
	}
	return configs
}

func loadLocalHTTPEndpointPath(log logger.Logger, runtimeID string, path string) []*httpEndpointV1alpha1.HTTPEndpoint {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}

	files, err := os.ReadDir(path)
	if err != nil {
		log.Errorf("Failed to read http endpoint files from path %s: %v", path, err)
		return nil
	}

	configs := make([]*httpEndpointV1alpha1.HTTPEndpoint, 0, len(files))

	type typeInfo struct {
		metav1.TypeMeta `json:",inline"`
	}

	for _, file := range files {
		if !utils.IsYaml(file.Name()) {
			log.Warnf("A non-YAML http endpoint file %s was detected, it will not be loaded", file.Name())
			continue
		}
		filePath := filepath.Join(path, file.Name())
		b, err := os.ReadFile(filePath)
		if err != nil {
			log.Errorf("Could not read http endpoint file %s: %v", file.Name(), err)
			continue
		}

		var ti typeInfo
		if err = yaml.Unmarshal(b, &ti); err != nil {
			log.Errorf("Could not determine resource type: %v", err)
			continue
		}

		if ti.Kind != httpEndpointKind {
			log.Errorf("invalid kind here %s", ti)
			continue
		}

		var endpoint httpEndpointV1alpha1.HTTPEndpoint
		if err = yaml.Unmarshal(b, &endpoint); err != nil {
			log.Errorf("Could not parse http endpoint file %s: %v", file.Name(), err)
			continue
		}
		configs = append(configs, &endpoint)
	}

	return configs
}
