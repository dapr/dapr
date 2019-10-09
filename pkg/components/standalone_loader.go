// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package components

import (
	"fmt"
	"io/ioutil"

	log "github.com/Sirupsen/logrus"
	components_v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	config "github.com/dapr/dapr/pkg/config/modes"
	"github.com/ghodss/yaml"
)

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
		if !file.IsDir() {
			b, err := ioutil.ReadFile(fmt.Sprintf("%s/%s", dir, file.Name()))
			if err != nil {
				log.Warnf("error reading file: %s", err)
				continue
			}

			var component components_v1alpha1.Component
			err = yaml.Unmarshal(b, &component)
			if err != nil {
				log.Warnf("error parsing file: %s", err)
				continue
			}

			list = append(list, component)
		}
	}

	return list, nil
}
