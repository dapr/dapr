package actors

// SaveStateRequest is the request object for saving an actor state
type SaveStateRequest struct {
	ActorID   string `json:"actorId"`
	ActorType string `json:"actorType"`
	Key       string `json:"key"`
	Data      []byte `json:"data"`
}
