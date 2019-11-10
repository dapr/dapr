// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package exporters

import (
	"fmt"
	"sync"

	"github.com/dapr/components-contrib/exporters"
)

// Registry is the interface for callers to get registered exporter components
type Registry interface {
	CreateExporter(name string) (exporters.Exporter, error)
}

type exporterRegistry struct {
	exporters map[string]func() exporters.Exporter
}

var instance *exporterRegistry
var once sync.Once

// NewRegistry returns a new exporter registry
func NewRegistry() Registry {
	once.Do(func() {
		instance = &exporterRegistry{
			exporters: map[string]func() exporters.Exporter{},
		}
	})
	return instance
}

// RegisterExporter registers a new exporter
func RegisterExporter(name string, factoryMethod func() exporters.Exporter) {
	instance.exporters[createFullName(name)] = factoryMethod
}

func createFullName(name string) string {
	return fmt.Sprintf("exporters.%s", name)
}

func (p *exporterRegistry) CreateExporter(name string) (exporters.Exporter, error) {
	if method, ok := p.exporters[name]; ok {
		return method(), nil
	}
	return nil, fmt.Errorf("couldn't find exporter %s", name)
}
