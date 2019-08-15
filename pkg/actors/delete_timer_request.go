package actors

// DeleteTimerRequest is a request object for deleting a timer
type DeleteTimerRequest struct {
	Name      string
	ActorType string
	ActorID   string
}
