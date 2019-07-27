package actors

// GetStateRequest is the request object for getting actor state
type GetStateRequest struct {
	ActorID   string `json:"actorId"`
	ActorType string `json:"actorType"`
}
