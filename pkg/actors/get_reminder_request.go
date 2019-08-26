package actors

// GetReminderRequest is the request object to get an existing reminder
type GetReminderRequest struct {
	Name      string
	ActorType string
	ActorID   string
}
