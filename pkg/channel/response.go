package channel

type InvokeResponse struct {
	Data     []byte            `json:"data"`
	Metadata map[string]string `json:"metadata"`
}
