package action

import (
	eventing_v1alpha1 "github.com/actionscore/actions/pkg/apis/eventing/v1alpha1"
)

type EventSource struct {
	Name   string          `json:"name"`
	Spec   EventSourceSpec `json:"spec"`
	Sender Sender
}

type EventSourceSpec struct {
	Type           string                          `json:"type"`
	ConnectionInfo interface{}                     `json:"connectionInfo"`
	SenderOptions  eventing_v1alpha1.SenderOptions `json:"senderOptions,omitempty"`
}
