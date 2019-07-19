package messaging

type DirectMessageRequest struct {
	Target   string            `json:"target"`
	Method   string            `json:"method"`
	From     string            `json:"from,omitempty"`
	Metadata map[string]string `json:"metadata"`
	Data     []byte            `json:"data,omitempty"`
}
