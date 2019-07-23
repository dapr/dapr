package components

import (
	"fmt"
	"io/ioutil"

	log "github.com/Sirupsen/logrus"
	config "github.com/actionscore/actions/pkg/config/modes"
	"github.com/ghodss/yaml"
)

type StandaloneComponents struct {
	config config.StandaloneConfig
}

func NewStandaloneComponents(configuration config.StandaloneConfig) *StandaloneComponents {
	return &StandaloneComponents{
		config: configuration,
	}
}

func (s *StandaloneComponents) LoadComponents() ([]Component, error) {
	dir := s.config.ComponentsPath
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	list := []Component{}

	for _, file := range files {
		if !file.IsDir() {
			b, err := ioutil.ReadFile(fmt.Sprintf("%s/%s", dir, file.Name()))
			if err != nil {
				log.Warnf("error reading file: %s", err)
				continue
			}

			var component Component
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
