/*
Copyright 2022 The Dapr Authors
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
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/apimachinery/pkg/api/validation/path"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/dapr/kit/utils"
)

const yamlSeparator = "\n---"

// manifestLoader loads a specific manifest kind from a folder.
type DiskManifestLoader[T kubernetesManifest] struct {
	zvFn  func() T
	kind  string
	paths []string
}

// NewDiskManifestLoader creates a new manifest loader for the given paths and kind.
func NewDiskManifestLoader[T kubernetesManifest](paths ...string) DiskManifestLoader[T] {
	var zero T
	return DiskManifestLoader[T]{
		paths: paths,
		kind:  zero.Kind(),
	}
}

// SetZeroValueFn sets the function that returns the "zero" object of the given type.
// This can be used to set default values before unmarshalling.
func (m *DiskManifestLoader[T]) SetZeroValueFn(zvFn func() T) {
	m.zvFn = zvFn
}

// load loads manifests for the given directory.
func (m DiskManifestLoader[T]) Load() ([]T, error) {
	manifests := []T{}
	for _, path := range m.paths {
		loaded, err := m.loadManifestsFromPath(path)
		if err != nil {
			return nil, err
		}
		if len(loaded) > 0 {
			manifests = append(manifests, loaded...)
		}
	}
	return manifests, nil
}

func (m DiskManifestLoader[T]) loadManifestsFromPath(path string) ([]T, error) {
	files, err := os.ReadDir(path)
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
			fileManifests := m.loadManifestsFromFile(filepath.Join(path, fileName))
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
		log.Warnf("daprd load %s error when reading file %s: %v", m.kind, manifestPath, err)
		return manifests
	}
	manifests, errors = m.decodeYaml(b)
	for _, err := range errors {
		log.Warnf("daprd load %s error when parsing manifests yaml resource in %s: %v", m.kind, manifestPath, err)
	}
	return manifests
}

type typeInfo struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
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

		if errs := path.IsValidPathSegmentName(ti.Name); len(errs) > 0 {
			errors = append(errors, fmt.Errorf("invalid name %q for %q: %s", ti.Name, m.kind, strings.Join(errs, "; ")))
			continue
		}

		var manifest T
		if m.zvFn != nil {
			manifest = m.zvFn()
		}
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
