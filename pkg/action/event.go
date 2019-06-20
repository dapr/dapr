package action

import "time"

type Event struct {
	EventName   string        `json:"eventName,omitempty"`
	To          []string      `json:"to,omitempty"`
	Concurrency string        `json:"concurrency,omitempty"`
	CreatedAt   time.Time     `json:"createdAt,omitempty"`
	State       []KeyValState `json:"state,omitempty"`
	Data        interface{}   `json:"data,omitempty"`
}
