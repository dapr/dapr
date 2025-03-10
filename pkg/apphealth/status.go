package apphealth

import "time"

const (
	AppStatusUnhealthy uint8 = 0
	AppStatusHealthy   uint8 = 1

	// reportMinInterval is the minimum interval between health reports.
	reportMinInterval = time.Second
)

type Status struct {
	State    uint8   `json:"state"`
	TimeUnix int64   `json:"timeUnix"`
	Reason   *string `json:"reason,omitempty"`
}

func (s *Status) IsHealthy() bool {
	return s.State == AppStatusHealthy
}

// DefaultStatus returns a default status for the app.
// This defaults to healthy so subscriptions can start right away.
func DefaultStatus() *Status {
	return &Status{
		State:    AppStatusHealthy,
		TimeUnix: time.Now().Unix(),
		Reason:   nil,
	}
}
