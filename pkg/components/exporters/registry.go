// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package exporters

import (
	"fmt"

	"github.com/dapr/components-contrib/exporters"
	"github.com/pkg/errors"
)

type (
	// Exporter is an exporter component definition.
	Exporter struct {
		Name          string
		FactoryMethod func() exporters.Exporter
	}

	// Registry is the interface for callers to get registered exporter components
	Registry interface {
		Register(components ...Exporter)
		Create(name string) (exporters.Exporter, error)
	}

	exporterRegistry struct {
		exporters map[string]func() exporters.Exporter
	}
)

// New creates an Exporter.
func New(name string, factoryMethod func() exporters.Exporter) Exporter {
	return Exporter{
		Name:          name,
		FactoryMethod: factoryMethod,
	}
}

// NewRegistry returns a new exporter registry.
func NewRegistry() Registry {
	return &exporterRegistry{
		exporters: map[string]func() exporters.Exporter{},
	}
}

// Register registers one or more new exporters.
func (p *exporterRegistry) Register(components ...Exporter) {
	for _, component := range components {
		p.exporters[createFullName(component.Name)] = component.FactoryMethod
	}
}

// Create instantiates an exporter based on `name`.
func (p *exporterRegistry) Create(name string) (exporters.Exporter, error) {
	if method, ok := p.exporters[name]; ok {
		return method(), nil
	}
	return nil, errors.Errorf("couldn't find exporter %s", name)
}

func createFullName(name string) string {
	return fmt.Sprintf("exporters.%s", name)
}
