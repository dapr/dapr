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

package disk

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/apimachinery/pkg/api/validation/path"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/utils"
)

const yamlSeparator = "\n---"

var log = logger.NewLogger("dapr.runtime.loader.disk")

type manifestSet[T meta.Resource] struct {
	d *disk[T]

	dirIndex      int
	fileIndex     int
	manifestIndex int

	order []manifestOrder
	ts    []T
}

type manifestOrder struct {
	dirIndex      int
	fileIndex     int
	manifestIndex int
}

func (m *manifestSet[T]) loadManifestsFromDirectory(dir string) error {
	defer func() {
		m.dirIndex++
	}()

	files, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	m.fileIndex = 0
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		fileName := file.Name()
		if !utils.IsYaml(fileName) {
			log.Warnf("A non-YAML %s file %s was detected, it will not be loaded", m.d.kind, fileName)
			continue
		}

		m.loadManifestsFromFile(filepath.Join(dir, fileName))
	}

	return nil
}

func (m *manifestSet[T]) loadManifestsFromFile(path string) {
	defer func() {
		m.fileIndex++
	}()

	f, err := os.Open(path)
	if err != nil {
		log.Warnf("daprd load %s error when opening file %s: %v", m.d.kind, path, err)
		return
	}
	defer f.Close()

	if err := m.decodeYaml(f); err != nil {
		log.Warnf("daprd load %s error when parsing manifests yaml resource in %s: %v", m.d.kind, path, err)
	}
}

func (m *manifestSet[T]) decodeYaml(f io.Reader) error {
	var errs []error
	scanner := bufio.NewScanner(f)
	scanner.Split(splitYamlDoc)

	m.manifestIndex = 0
	for {
		m.manifestIndex++

		if !scanner.Scan() {
			err := scanner.Err()
			if err != nil {
				errs = append(errs, err)
				continue
			}

			break
		}

		scannerBytes := scanner.Bytes()
		var ti struct {
			metav1.TypeMeta   `json:",inline"`
			metav1.ObjectMeta `json:"metadata,omitempty"`
		}
		if err := yaml.Unmarshal(scannerBytes, &ti); err != nil {
			errs = append(errs, err)
			continue
		}

		if ti.APIVersion != m.d.apiVersion || ti.Kind != m.d.kind {
			continue
		}

		if patherrs := path.IsValidPathSegmentName(ti.Name); len(patherrs) > 0 {
			errs = append(errs, fmt.Errorf("invalid name %q for %q: %s", ti.Name, m.d.kind, strings.Join(patherrs, "; ")))
			continue
		}

		var manifest T
		if err := yaml.Unmarshal(scannerBytes, &manifest); err != nil {
			errs = append(errs, err)
			continue
		}

		m.order = append(m.order, manifestOrder{
			dirIndex:      m.dirIndex,
			fileIndex:     m.fileIndex,
			manifestIndex: m.manifestIndex - 1,
		})
		m.ts = append(m.ts, manifest)
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
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

func (m *manifestSet[T]) Len() int {
	return len(m.order)
}

func (m *manifestSet[T]) Swap(i, j int) {
	m.order[i], m.order[j] = m.order[j], m.order[i]
	m.ts[i], m.ts[j] = m.ts[j], m.ts[i]
}

func (m *manifestSet[T]) Less(i, j int) bool {
	return m.order[i].dirIndex < m.order[j].dirIndex ||
		m.order[i].fileIndex < m.order[j].fileIndex ||
		m.order[i].manifestIndex < m.order[j].manifestIndex
}
