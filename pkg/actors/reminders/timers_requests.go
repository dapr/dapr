package reminders

import "encoding/json"

// TimerResponse is the response object send to an Actor SDK API when a timer fires.
type TimerResponse struct {
	Callback string `json:"callback"`
	Data     any    `json:"data"`
	DueTime  string `json:"dueTime"`
	Period   string `json:"period"`
}

// MarshalJSON is a custom JSON marshaler that encodes the data as JSON.
// Actor SDKs expect "data" to be a base64-encoded message with the JSON representation of the data, so this makes sure that happens.
// This method implements the json.Marshaler interface.
func (t *TimerResponse) MarshalJSON() ([]byte, error) {
	type responseAlias TimerResponse
	m := struct {
		Data any `json:"data,omitempty"`
		*responseAlias
	}{
		responseAlias: (*responseAlias)(t),
	}

	m.Data = t.Data
	return json.Marshal(m)
}
