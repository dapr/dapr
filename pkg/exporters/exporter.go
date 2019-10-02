package exporters

// Exporter is an interface for an Dapr metrics exporter
type Exporter interface {
	Init(daprID string, daprAddress string, exporterAddress string) error
}
