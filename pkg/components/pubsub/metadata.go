package pubsub

type Metadata struct {
	ConnectionInfo map[string]string `json:"connectionInfo"`
	Properties     map[string]string `json:"properties"`
}
