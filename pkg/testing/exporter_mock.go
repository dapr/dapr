package testing

import (
	"github.com/dapr/components-contrib/exporters"
	mock "github.com/stretchr/testify/mock"
)

// MockExporter is a mock exporter object
type MockExporter struct {
	mock.Mock
}

// Init is a mock initialization method
func (m *MockExporter) Init(daprID string, hostAddress string, metadata exporters.Metadata) error {
	args := m.Called(daprID, hostAddress, metadata)
	return args.Error(0)
}
