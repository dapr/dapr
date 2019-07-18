package state

type GetRequest struct {
	Key  string `json:"key"`
	ETag string `json:"etag,omitempty"`
}

type DeleteRequest struct {
	Key string `json:"key"`
}

type SetRequest struct {
	Key      string            `json:"key"`
	Value    interface{}       `json:"value"`
	ETag     string            `json:"etag,omitempty"`
	Metadata map[string]string `json:"metadata"`
}
