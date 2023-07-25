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

package internal

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	timeutils "github.com/dapr/kit/time"
)

// ReminderPeriod contains the parsed period for a reminder.
type ReminderPeriod struct {
	value string // Raw value as received from the user

	years   int
	months  int
	days    int
	period  time.Duration
	repeats int
}

// NewReminderPeriod parses a reminder period from a string and validates it.
func NewReminderPeriod(val string) (p ReminderPeriod, err error) {
	p = NewEmptyReminderPeriod()

	if val != "" {
		p.value = val
		err = parseReminderPeriod(&p)
	}

	return p, err
}

// NewEmptyReminderPeriod returns an empty ReminderPeriod, which has unlimited repeats.
func NewEmptyReminderPeriod() ReminderPeriod {
	return ReminderPeriod{
		repeats: -1,
	}
}

// HasRepeats returns true if the period will repeat.
func (p ReminderPeriod) HasRepeats() bool {
	return p.repeats != 0 &&
		(p.years != 0 || p.months != 0 || p.days != 0 || p.period != 0)
}

// GetNext returns the next time the periodic reminder should fire after a given time.
func (p ReminderPeriod) GetFollowing(t time.Time) time.Time {
	return t.AddDate(p.years, p.months, p.days).Add(p.period)
}

// String implements fmt.Stringer. It returns the value.
func (p ReminderPeriod) String() string {
	return p.value
}

func (p ReminderPeriod) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.value)
}

func (p *ReminderPeriod) UnmarshalJSON(data []byte) error {
	*p = ReminderPeriod{
		value:   string(data),
		repeats: -1,
	}

	// Handle nulls and other empty values
	switch p.value {
	case "", "null", "{}", `""`, `[]`:
		p.value = ""
		return nil
	}

	// Remove quotes if present
	if len(p.value) >= 2 && p.value[0] == '"' && p.value[len(p.value)-1] == '"' {
		p.value = p.value[1 : len(p.value)-1]
	}

	return parseReminderPeriod(p)
}

func parseReminderPeriod(p *ReminderPeriod) (err error) {
	p.years, p.months, p.days, p.period, p.repeats, err = timeutils.ParseDuration(p.value)
	if err != nil {
		return fmt.Errorf("parse error: %w", err)
	}

	// Error on timers with zero repetitions
	if p.repeats == 0 {
		return errors.New("has zero repetitions")
	}

	return nil
}
