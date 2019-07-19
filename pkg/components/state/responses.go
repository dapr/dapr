package state

type GetResponse struct {
	Data     []byte            `json:"data"`
	ETag     string            `json:"etag,omitempty"`
	Metadata map[string]string `json:"metadata"`
}
