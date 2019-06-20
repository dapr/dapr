package action

type Configuration struct {
	Spec ConfigurationSpec `json:"spec,omitempty"`
}

type ConfigurationSpec struct {
	TracingSpec TracingSpec `json:"tracing,omitempty"`
}

type TracingSpec struct {
	Enabled          bool   `json:"enabled"`
	ExporterType     string `json:"exporterType"`
	ExporterAddress  string `json:"exporterAddress"`
	IncludeEvent     bool   `json:"includeEvent"`
	IncludeEventBody bool   `json:"includeEventBody"`
}
