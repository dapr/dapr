package actors

// DeleteStateRequest is the request object for deleting an actor state
type DeleteStateRequest struct {
	ActorID   string `json:"actorId"`
	ActorType string `json:"actorType"`
	Key       string `json:"key"`
}
