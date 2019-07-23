package actors

type CallRequest struct {
	ActorType string            `json:"actorType"`
	ActorID   string            `json:"actorId"`
	Method    string            `json:"method"`
	Data      []byte            `json:"data"`
	Metadata  map[string]string `json:"metadata"`
}
