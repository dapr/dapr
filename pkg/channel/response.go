package channel

// InvokeResponse is the response object from invoking user code
type InvokeResponse struct {
	Data     []byte            `json:"data"`
	Metadata map[string]string `json:"metadata"`
}
