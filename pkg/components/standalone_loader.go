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

package components

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"

	"github.com/ghodss/yaml"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	components_v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	config "github.com/dapr/dapr/pkg/config/modes"
)

const (
	yamlSeparator = "\n---"
	componentKind = "Component"
)

// StandaloneComponents loads components in a standalone mode environment.
type StandaloneComponents struct {
	config config.StandaloneConfig
}

// NewStandaloneComponents returns a new standalone loader.
func NewStandaloneComponents(configuration config.StandaloneConfig) *StandaloneComponents {
	return &StandaloneComponents{
		config: configuration,
	}
}

// LoadComponents loads dapr components from a given directory.
func (s *StandaloneComponents) LoadComponents() ([]components_v1alpha1.Component, error) {
	files, err := os.ReadDir(s.config.ComponentsPath)
	if err != nil {
		return nil, err
	}

	list := []components_v1alpha1.Component{}

	for _, file := range files {
		if !file.IsDir() && s.isYaml(file.Name()) {
			components := s.loadComponentsFromFile(file.Name())
			if len(components) > 0 {
				list = append(list, components...)
			}
		}
	}

	return list, nil
}

func (s *StandaloneComponents) loadComponentsFromFile(filename string) []components_v1alpha1.Component {
	var errors []error

	components := []components_v1alpha1.Component{}
	path := filepath.Join(s.config.ComponentsPath, filename)

	b, err := os.ReadFile(path)
	if err != nil {
		log.Warnf("daprd load components error when reading file %s : %s", path, err)
		return components
	}
	components, errors = s.decodeYaml(b)
	for _, err := range errors {
		log.Warnf("daprd load components error when parsing components yaml resource in %s : %s", path, err)
	}
	return components
}

// isYaml checks whether the file is yaml or not.
func (s *StandaloneComponents) isYaml(fileName string) bool {
	extension := strings.ToLower(filepath.Ext(fileName))
	if extension == ".yaml" || extension == ".yml" {
		return true
	}
	return false
}

// decodeYaml decodes the yaml document.
func (s *StandaloneComponents) decodeYaml(b []byte) ([]components_v1alpha1.Component, []error) {
	list := []components_v1alpha1.Component{}
	errors := []error{}
	scanner := bufio.NewScanner(bytes.NewReader(b))
	scanner.Split(s.splitYamlDoc)

	type typeInfo struct {
		metav1.TypeMeta `json:",inline"`
	}

	for {
		if !scanner.Scan() {
			err := scanner.Err()
			if err != nil {
				errors = append(errors, err)

				continue
			}

			break
		}

		scannerBytes := scanner.Bytes()
		var ti typeInfo
		if err := yaml.Unmarshal(scannerBytes, &ti); err != nil {
			errors = append(errors, err)

			continue
		}

		if ti.Kind != componentKind {
			continue
		}

		var comp components_v1alpha1.Component
		comp.Spec = components_v1alpha1.ComponentSpec{}
		if err := yaml.Unmarshal(scannerBytes, &comp); err != nil {
			errors = append(errors, err)

			continue
		}

		list = append(list, comp)
	}

	return list, errors
}

// splitYamlDoc - splits the yaml docs.
func (s *StandaloneComponents) splitYamlDoc(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	sep := len([]byte(yamlSeparator))
	if i := bytes.Index(data, []byte(yamlSeparator)); i >= 0 {
		i += sep
		after := data[i:]

		if len(after) == 0 {
			if atEOF {
				return len(data), data[:len(data)-sep], nil
			}
			return 0, nil, nil
		}
		if j := bytes.IndexByte(after, '\n'); j >= 0 {
			return i + j + 1, data[0 : i-sep], nil
		}
		return 0, nil, nil
	}
	// If we're at EOF, we have a final, non-terminated line. Return it.
	if atEOF {
		return len(data), data, nil
	}
	// Request more data.
	return 0, nil, nil
}
