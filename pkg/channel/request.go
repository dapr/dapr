package channel

type InvokeRequest struct {
	Method   string            `json:"method"`
	Payload  []byte            `json:"payload"`
	Metadata map[string]string `json:"metadata"`
}
