package exporters

type Exporter interface {
	Init(action_id string, action_address string, exporter_address string) error
}
