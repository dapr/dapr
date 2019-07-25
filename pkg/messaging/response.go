package messaging

// DirectMessageResponse is the response object for directly invoking a remote app
type DirectMessageResponse struct {
	Data     []byte            `json:"data"`
	Metadata map[string]string `json:"metadata"`
}
