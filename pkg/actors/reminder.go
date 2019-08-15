package actors

// Reminder represents a persisted reminder for a unique actor
type Reminder struct {
	ActorID        string      `json:"actorID"`
	ActorType      string      `json:"actorType"`
	Name           string      `json:"name"`
	Data           interface{} `json:"data"`
	Period         string      `json:"period"`
	DueTime        string      `json:"dueTime"`
	RegisteredTime string      `json:"registeredTime"`
}
