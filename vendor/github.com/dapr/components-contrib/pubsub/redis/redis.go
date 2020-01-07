// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package redis

import (
	"crypto/tls"
	"errors"
	"fmt"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/dapr/components-contrib/pubsub"
	"github.com/go-redis/redis"
)

const (
	host       = "redisHost"
	password   = "redisPassword"
	consumerID = "consumerID"
	enableTLS  = "enableTLS"
)

type redisStreams struct {
	metadata metadata
	client   *redis.Client
}

// NewRedisStreams returns a new redis streams pub-sub implementation
func NewRedisStreams() pubsub.PubSub {
	return &redisStreams{}
}

func parseRedisMetadata(meta pubsub.Metadata) (metadata, error) {
	m := metadata{}
	if val, ok := meta.Properties[host]; ok && val != "" {
		m.host = val
	} else {
		return m, errors.New("redis streams error: missing host address")
	}

	if val, ok := meta.Properties[password]; ok && val != "" {
		m.password = val
	}

	if val, ok := meta.Properties[enableTLS]; ok && val != "" {
		tls, err := strconv.ParseBool(val)
		if err != nil {
			return m, fmt.Errorf("redis streams error: can't parse enableTLS field: %s", err)
		}
		m.enableTLS = tls
	}

	if val, ok := meta.Properties[consumerID]; ok && val != "" {
		m.consumerID = val
	} else {
		return m, errors.New("redis streams error: missing consumerID")
	}

	return m, nil
}

func (r *redisStreams) Init(metadata pubsub.Metadata) error {
	m, err := parseRedisMetadata(metadata)
	if err != nil {
		return err
	}
	r.metadata = m

	options := &redis.Options{
		Addr:            m.host,
		Password:        m.password,
		DB:              0,
		MaxRetries:      3,
		MaxRetryBackoff: time.Second * 2,
	}

	/* #nosec */
	if r.metadata.enableTLS {
		options.TLSConfig = &tls.Config{
			InsecureSkipVerify: r.metadata.enableTLS,
		}
	}

	client := redis.NewClient(options)

	_, err = client.Ping().Result()
	if err != nil {
		return fmt.Errorf("redis streams: error connecting to redis at %s: %s", m.host, err)
	}

	r.client = client
	return nil
}

func (r *redisStreams) Publish(req *pubsub.PublishRequest) error {
	_, err := r.client.XAdd(&redis.XAddArgs{
		Stream: req.Topic,
		Values: map[string]interface{}{"data": req.Data},
	}).Result()
	if err != nil {
		return fmt.Errorf("redis streams: error from publish: %s", err)
	}

	return nil
}

func (r *redisStreams) Subscribe(req pubsub.SubscribeRequest, handler func(msg *pubsub.NewMessage) error) error {
	err := r.client.XGroupCreateMkStream(req.Topic, r.metadata.consumerID, "0").Err()
	if err != nil {
		log.Warnf("redis streams: %s", err)
	}
	go r.beginReadingFromStream(req.Topic, r.metadata.consumerID, handler)
	return nil
}

func (r *redisStreams) readFromStream(stream, consumerID, start string) ([]redis.XStream, error) {
	res, err := r.client.XReadGroup(&redis.XReadGroupArgs{
		Group:    consumerID,
		Consumer: consumerID,
		Streams:  []string{stream, start},
	}).Result()
	if err != nil {
		return nil, err
	}

	return res, nil
}

func (r *redisStreams) processStreams(consumerID string, streams []redis.XStream, handler func(msg *pubsub.NewMessage) error) {
	for _, s := range streams {
		for _, m := range s.Messages {
			msg := pubsub.NewMessage{
				Topic: s.Stream,
			}
			data, exists := m.Values["data"]
			if exists && data != nil {
				msg.Data = []byte(data.(string))
			}

			err := handler(&msg)
			if err == nil {
				r.client.XAck(s.Stream, consumerID, m.ID).Result()
			}
		}
	}
}

func (r *redisStreams) beginReadingFromStream(stream, consumerID string, handler func(msg *pubsub.NewMessage) error) {
	// first read pending items in case of recovering from crash
	start := "0"

	for {
		streams, err := r.readFromStream(stream, consumerID, start)
		if err != nil {
			log.Errorf("redis streams: error reading from stream %s: %s", stream, err)
			return
		}
		r.processStreams(consumerID, streams, handler)

		//continue with new non received items
		start = ">"
	}
}
