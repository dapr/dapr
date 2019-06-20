package action

import (
	"context"
	"time"

	eventing_v1alpha1 "github.com/actionscore/actions/pkg/apis/eventing/v1alpha1"
)

type Sender interface {
	//Ensure makes sure a queue name exists
	Init(spec EventSourceSpec) error
	//Enqueue an event to the sending queue
	Enqueue(event Event) error
	//Dequeue an event from the sending queue
	Dequeue(workerIndex int, events []Event) error
	//Get a lock on queue header and return the header element
	PeekLock(workerIndex int) (*[]Event, error)
	//Start message sending loop
	StartLoop(postFunc func(event *[]Event, context context.Context) error, context context.Context)

	ReadAsync(metadata interface{}, callback func([]byte) error) error
	Read(metadata interface{}) (interface{}, error)
	Write(data interface{}) error
}

//batch modes
const WindowedBatch = "windowed"
const FixedSizeBatch = "fixsize"
const NoBatch = "no"

//dedup modes
const NoDedup = "no"
const WindowedDedup = "windowed"

//order modes
const NoOrder = "no"
const FIFO = "fifo"
const Sorted = "sorted"

func NewRedisSender() Sender {
	return &RedisSender{
		LastSentBatch: time.Time{},
	}
}
func DefaultSenderOptions() eventing_v1alpha1.SenderOptions {
	return eventing_v1alpha1.SenderOptions{
		NumWorkers: 10,
		BatchOptions: eventing_v1alpha1.BatchOptions{
			BatchMode: FixedSizeBatch,
			BatchSize: 10,
		},
	}
}
