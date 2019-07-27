package actors

// CallResponse is the response object returned by an actor call
type CallResponse struct {
	Data     []byte            `json:"data"`
	Metadata map[string]string `json:"metadata"`
}
