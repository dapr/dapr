// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package components

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"path/filepath"
	"strings"

	components_v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	config "github.com/dapr/dapr/pkg/config/modes"
	"github.com/ghodss/yaml"
)

const yamlSeparator = "\n---"

// StandaloneComponents loads components in a standalone mode environment
type StandaloneComponents struct {
	config config.StandaloneConfig
}

// NewStandaloneComponents returns a new standalone loader
func NewStandaloneComponents(configuration config.StandaloneConfig) *StandaloneComponents {
	return &StandaloneComponents{
		config: configuration,
	}
}

// LoadComponents loads dapr components from a given directory
func (s *StandaloneComponents) LoadComponents() ([]components_v1alpha1.Component, error) {
	dir := s.config.ComponentsPath
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	list := []components_v1alpha1.Component{}

	for _, file := range files {
		if !file.IsDir() && s.isYaml(file.Name()) {
			b, err := ioutil.ReadFile(fmt.Sprintf("%s/%s", dir, file.Name()))

			if err != nil {
				log.Warnf("error reading file %s/%s : %s", dir, file.Name(), err)
				continue
			}

			components, _ := s.decodeYaml(fmt.Sprintf("%s/%s", dir, file.Name()), b)
			list = append(list, components...)
		}
	}

	return list, nil
}

// isYaml checks whether the file is yaml or not
func (s *StandaloneComponents) isYaml(fileName string) bool {
	extension := strings.ToLower(filepath.Ext(fileName))
	if extension == ".yaml" || extension == ".yml" {
		return true
	}
	return false
}

// decodeYaml decodes the yaml document
func (s *StandaloneComponents) decodeYaml(filename string, b []byte) ([]components_v1alpha1.Component, []error) {
	list := []components_v1alpha1.Component{}
	errors := []error{}
	scanner := bufio.NewScanner(bytes.NewReader(b))
	scanner.Split(s.splitYamlDoc)

	for {
		var component components_v1alpha1.Component
		err := s.decode(scanner, &component)
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Warnf("error parsing yaml resource in %s : %s", filename, err)
			errors = append(errors, err)
			continue
		}
		list = append(list, component)
	}

	return list, errors
}

// decode reads the YAML resource in document
func (s *StandaloneComponents) decode(scanner *bufio.Scanner, c interface{}) error {
	if scanner.Scan() {
		return yaml.Unmarshal(scanner.Bytes(), &c)
	}

	err := scanner.Err()
	if err == nil {
		err = io.EOF
	}
	return err
}

// splitYamlDoc - splits the yaml docs
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
