package bindings

type Metadata struct {
	Name           string
	ConnectionInfo map[string]string `json:"connectionInfo"`
	Properties     map[string]string `json:"properties"`
}
