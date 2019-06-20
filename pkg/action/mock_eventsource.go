package action

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

type MockEventSource struct {
	Spec EventSourceSpec
}

type MockMetadata struct {
	MessageCount string `json:"messageCount,omitempty"`
	Interval     string `json:"interval,omitempty"`
}

func NewMockEventSource() *MockEventSource {
	return &MockEventSource{}
}

func (a *MockEventSource) Init(eventSourceSpec EventSourceSpec) error {
	a.Spec = eventSourceSpec
	return nil
}

func (a *MockEventSource) Write(data interface{}) error {
	_, err := json.Marshal(a.Spec.ConnectionInfo)
	if err != nil {
		return err
	}

	_, err = json.Marshal(data)
	if err != nil {
		return err
	}

	return nil
}

func (a *MockEventSource) Read(metadata interface{}) (interface{}, error) {
	return nil, nil
}

func (a *MockEventSource) ReadAsync(metadata interface{}, callback func([]byte) error) error {
	b, err := json.Marshal(a.Spec.ConnectionInfo)
	if err != nil {
		return err
	}
	var mockmeta MockMetadata
	err = json.Unmarshal(b, &mockmeta)
	if err != nil {
		return err
	}
	count := 0
	numInterval, _ := strconv.Atoi(mockmeta.Interval)
	numMessageCount, _ := strconv.Atoi(mockmeta.MessageCount)
	if numInterval == 0 {
		numInterval = 50
	}
	for {
		body := fmt.Sprintf("{\"time\":\"%s\"}", time.Now())
		fmt.Println(body + "#" + time.Now().Format(time.RFC3339))
		callback([]byte(body))
		time.Sleep(time.Duration(numInterval) * time.Millisecond)
		count++
		if numMessageCount > 0 && count >= numMessageCount {
			break
		}
	}

	return nil
}
