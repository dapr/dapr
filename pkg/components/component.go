package components

type Component struct {
	Metadata ComponentMetadata `json:"metadata"`
	Spec     ComponentSpec     `json:"spec"`
}

type ComponentMetadata struct {
	Name string `json:"name"`
}

type ComponentSpec struct {
	Type           string            `json:"type"`
	ConnectionInfo map[string]string `json:"connectionInfo"`
	Properties     map[string]string `json:"properties"`
}
