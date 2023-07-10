/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package reminder

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	timeutils "github.com/dapr/kit/time"
)

// ReminderPeriod contains the parsed period for a reminder.
type ReminderPeriod struct {
	Value string // Raw value as received from the user

	Years   int
	Months  int
	Days    int
	Period  time.Duration
	Repeats int
}

// NewReminderPeriod parses a reminder period from a string and validates it.
func NewReminderPeriod(val string) (p ReminderPeriod, err error) {
	p = NewEmptyReminderPeriod()

	if val != "" {
		p.Value = val
		err = parseReminderPeriod(&p)
	}

	return p, err
}

// NewEmptyReminderPeriod returns an empty ReminderPeriod, which has unlimited repeats.
func NewEmptyReminderPeriod() ReminderPeriod {
	return ReminderPeriod{
		Repeats: -1,
	}
}

// HasRepeats returns true if the period will repeat.
func (p ReminderPeriod) HasRepeats() bool {
	return p.Repeats != 0 &&
		(p.Years != 0 || p.Months != 0 || p.Days != 0 || p.Period != 0)
}

// GetNext returns the next time the periodic reminder should fire after a given time.
func (p ReminderPeriod) GetFollowing(t time.Time) time.Time {
	return t.AddDate(p.Years, p.Months, p.Days).Add(p.Period)
}

// String implements fmt.Stringer. It returns the value.
func (p ReminderPeriod) String() string {
	return p.Value
}

func (p ReminderPeriod) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.Value)
}

func (p *ReminderPeriod) UnmarshalJSON(data []byte) error {
	*p = ReminderPeriod{
		Value:   string(data),
		Repeats: -1,
	}

	// Handle nulls and other empty values
	switch p.Value {
	case "", "null", "{}", `""`, `[]`:
		p.Value = ""
		return nil
	}

	// Remove quotes if present
	if len(p.Value) >= 2 && p.Value[0] == '"' && p.Value[len(p.Value)-1] == '"' {
		p.Value = p.Value[1 : len(p.Value)-1]
	}

	return parseReminderPeriod(p)
}

func parseReminderPeriod(p *ReminderPeriod) (err error) {
	p.Years, p.Months, p.Days, p.Period, p.Repeats, err = timeutils.ParseDuration(p.Value)
	if err != nil {
		return fmt.Errorf("parse error: %w", err)
	}

	// Error on timers with zero repetitions
	if p.Repeats == 0 {
		return errors.New("has zero repetitions")
	}

	return nil
}
