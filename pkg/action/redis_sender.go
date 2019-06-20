package action

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	log "github.com/Sirupsen/logrus"
	eventing_v1alpha1 "github.com/actionscore/actions/pkg/apis/eventing/v1alpha1"
	"github.com/go-redis/redis"
)

type RedisSender struct {
	InboxQueue    string
	Client        *redis.Client
	Options       eventing_v1alpha1.SenderOptions
	BatchBuffer   []Event
	LastSentBatch time.Time
}

func (s *RedisSender) MarshalEvents(events []Event) ([]byte, error) {
	b, err := json.Marshal(events)
	if err != nil {
		return nil, err
	}
	return b, nil
}
func (s *RedisSender) MarshalEvent(event Event) ([]byte, error) {
	return s.MarshalEvents([]Event{event})
}
func (s *RedisSender) UnMarshalEvents(value string) ([]Event, error) {
	var events []Event
	if err := json.Unmarshal([]byte(value), &events); err != nil {
		return nil, err
	}
	return events, nil
}
func (s *RedisSender) Enqueue(event Event) error {
	s.BatchBuffer = append(s.BatchBuffer, event)
	if len(s.BatchBuffer) >= s.Options.SendBufferSize {
		b, err := s.MarshalEvents(s.BatchBuffer)
		if err != nil {
			return err
		}
		s.BatchBuffer = nil
		s.LastSentBatch = time.Now()
		return s.Client.LPush(s.InboxQueue, b).Err()
	}
	return nil
}

func (s *RedisSender) Dequeue(workerIndex int, events []Event) error {
	queueName := fmt.Sprintf("%s%s_%d", s.InboxQueue, "_processing", workerIndex)
	b, err := s.MarshalEvents(events)
	if err != nil {
		return err
	}
	_, err = s.Client.LRem(queueName, -1, b).Result()
	return err
}
func (s *RedisSender) PeekLock(workerIndex int) (*[]Event, error) {
	queueName := fmt.Sprintf("%s%s_%d", s.InboxQueue, "_processing", workerIndex)
	_, err := s.Client.RPopLPush(s.InboxQueue, queueName).Result()
	v, err := s.Client.LRange(queueName, -1, -1).Result()
	if err != nil {
		return nil, err
	}
	if len(v) == 1 {
		var events []Event
		events, err = s.UnMarshalEvents(v[0])
		if err != nil {
			return nil, err
		}
		return &events, nil
	} else {
		return nil, nil
	}
}
func (s *RedisSender) Init(spec EventSourceSpec) error {
	s.Options = spec.SenderOptions
	s.InboxQueue = spec.SenderOptions.QueueName
	connInfo := spec.ConnectionInfo
	b, err := json.Marshal(connInfo)
	if err != nil {
		return err
	}

	var redisCreds RedisCredentials
	err = json.Unmarshal(b, &redisCreds)
	if err != nil {
		return err
	}

	s.Client = redis.NewClient(&redis.Options{
		Addr:     redisCreds.Host,
		Password: redisCreds.Password,
		DB:       0,
	})
	_, err = s.Client.Ping().Result()
	if err != nil {
		return err
	}

	return nil
}
func (b *RedisSender) StartLoop(postFunc func(events *[]Event, ctx1 context.Context) error, ctx context.Context) {
	for i := 0; i < b.Options.NumWorkers; i++ {
		go func(index int, ctx context.Context) {
			for {
				evts, err := b.PeekLock(index)
				if err != nil {
					log.Errorf("Error locking event - %s", err)
				}
				if evts != nil {
					err := postFunc(evts, ctx)
					if err == nil {
						err = b.Dequeue(index, *evts)
						if err != nil {
							log.Errorf("Error dequeuing event - %s", err)
						}
					} else {
						log.Errorf("Error posting event - %s", err)
					}
				}
				time.Sleep(time.Millisecond * 50)
			}
		}(i, ctx)
	}
}
func (m *RedisSender) ReadAsync(metadata interface{}, callback func([]byte) error) error {
	return nil
}

func (m *RedisSender) Read(metadata interface{}) (interface{}, error) {
	return nil, nil
}

func (m *RedisSender) Write(data interface{}) error {
	return nil
}
