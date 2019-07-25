package exporters

// Exporter is an interface for an Actions metrics exporter
type Exporter interface {
	Init(actionsID string, actionsAddress string, exporterAddress string) error
}
