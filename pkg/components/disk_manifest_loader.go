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

	"github.com/dapr/dapr/utils"

	"github.com/ghodss/yaml"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const yamlSeparator = "\n---"

// manifestLoader loads a specific manifest kind from a folder.
type DiskManifestLoader[T any] struct {
	zv   func() T
	kind string
	path string
}

// NewDiskManifestLoader creates a new manifest loader for the given path and kind.
func NewDiskManifestLoader[T any](path, kind string, zeroValue func() T) DiskManifestLoader[T] {
	return DiskManifestLoader[T]{
		path: path,
		kind: kind,
		zv:   zeroValue,
	}
}

// load loads manifests for the given directory.
func (m DiskManifestLoader[T]) Load() ([]T, error) {
	files, err := os.ReadDir(m.path)
	if err != nil {
		return nil, err
	}

	manifests := make([]T, 0)

	for _, file := range files {
		if !file.IsDir() {
			fileName := file.Name()
			if !utils.IsYaml(fileName) {
				log.Warnf("A non-YAML %s file %s was detected, it will not be loaded", m.kind, fileName)
				continue
			}
			fileManifests := m.loadManifestsFromFile(filepath.Join(m.path, fileName))
			manifests = append(manifests, fileManifests...)
		}
	}

	return manifests, nil
}

func (m DiskManifestLoader[T]) loadManifestsFromFile(manifestPath string) []T {
	var errors []error

	manifests := make([]T, 0)
	b, err := os.ReadFile(manifestPath)
	if err != nil {
		log.Warnf("daprd load %s error when reading file %s : %s", m.kind, manifestPath, err)
		return manifests
	}
	manifests, errors = m.decodeYaml(b)
	for _, err := range errors {
		log.Warnf("daprd load %s error when parsing manifests yaml resource in %s : %s", m.kind, manifestPath, err)
	}
	return manifests
}

type typeInfo struct {
	metav1.TypeMeta `json:",inline"`
}

// decodeYaml decodes the yaml document.
func (m DiskManifestLoader[T]) decodeYaml(b []byte) ([]T, []error) {
	list := make([]T, 0)
	errors := []error{}
	scanner := bufio.NewScanner(bytes.NewReader(b))
	scanner.Split(splitYamlDoc)

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

		if ti.Kind != m.kind {
			continue
		}

		manifest := m.zv()
		if err := yaml.Unmarshal(scannerBytes, &manifest); err != nil {
			errors = append(errors, err)

			continue
		}

		list = append(list, manifest)
	}

	return list, errors
}

// splitYamlDoc - splits the yaml docs.
func splitYamlDoc(data []byte, atEOF bool) (advance int, token []byte, err error) {
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
