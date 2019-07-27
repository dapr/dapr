package messaging

// DirectMessageRequest is the request object for directly invoking a remote app
type DirectMessageRequest struct {
	Target   string            `json:"target"`
	Method   string            `json:"method"`
	From     string            `json:"from,omitempty"`
	Metadata map[string]string `json:"metadata"`
	Data     []byte            `json:"data,omitempty"`
}
