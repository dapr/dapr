package v1alpha1

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:noStatus
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// EventSource describes an Action event source
type EventSource struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +optional
	Spec EventSourceSpec `json:"spec,omitempty"`
}

// EventSourceSpec is the spec for an EventSource
type EventSourceSpec struct {
	Type string `json:"type"`
	// +k8s:deepcopy-gen=false
	ConnectionInfo map[string]string `json:"connectionInfo"`
	SenderOptions  SenderOptions     `json:"senderOptions,omitempty"`
}

type SenderOptions struct {
	BatchOptions   BatchOptions `json:"batchOptions,omitempty"`
	DedupOptions   DedupOptions `json:"dedupOptions,omitempty"`
	NumWorkers     int          `json:"numWorkers,omitempty"`
	SendBufferSize int          `json:"sendBufferSize,omitempty"`
	QueueName      string       `json:"queueName,omitempty"`
}

type BatchOptions struct {
	BatchMode  string        `json:"mode"`
	BatchSize  int           `json:"size,omitempty"`
	WindowSize time.Duration `json:"window,omitempty"`
}

type DedupOptions struct {
	DedupMode   string        `json:"mode"`
	DedupWindow time.Duration `json:"window,omitempty"`
	DedupField  string        `json:"field,omitempty"`
}
type OrderOptions struct {
	OrderMode  string        `json:"mode"`
	SortWindow time.Duration `json:"window,omitempty"`
	SortField  string        `json:"field,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// EventSourceList is a list of Action event sources
type EventSourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []EventSource `json:"items"`
}
