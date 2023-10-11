/*
Copyright 2022 The Dapr Authors
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

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/bsm/redislock"
	redis "github.com/go-redis/redis/v9"

	"github.com/dapr/go-sdk/actor"
	dapr "github.com/dapr/go-sdk/client"
	daprd "github.com/dapr/go-sdk/service/http"
)

func testActorFactory(client dapr.Client, redisClient *redis.Client) func() actor.ServerContext {
	lockClient := redislock.New(redisClient)
	return func() actor.ServerContext {
		return &TestActor{
			daprClient: client,
			locker:     lockClient,
		}
	}
}

type TestActor struct {
	actor.ServerImplBaseCtx
	daprClient dapr.Client
	locker     *redislock.Client
}

func (t *TestActor) Type() string {
	return "fake-actor-type"
}

// user defined functions
func (t *TestActor) Lock(ctx context.Context, req any) (any, error) {
	lockTimeout := time.Second
	// Try to obtain lock.
	lock, err := t.locker.Obtain(ctx, fmt.Sprintf("DOUBLE_ACTIVATION_ACTOR_TEST_%s", t.ID()), lockTimeout, nil)
	if err == redislock.ErrNotObtained {
		return nil, errors.New("resource was locked!")
	}

	if err == nil {
		if releaseErr := lock.Release(ctx); releaseErr != nil {
			time.Sleep(lockTimeout * 2) // sleep to make sure that the lock will be automatically released
		}
	}
	return "succeed", nil
}

func main() {
	client, err := dapr.NewClient()
	if err != nil {
		panic(err)
	}
	defer client.Close()
	m, err := client.GetSecret(context.Background(), "kubernetes", "redissecret", map[string]string{})
	if err != nil {
		panic(err)
	}
	redisHost := m["host"]
	if len(redisHost) == 0 {
		panic(errors.New("redis host not provided"))
	}
	// Connect to redis.
	redisClient := redis.NewClient(&redis.Options{
		Network: "tcp",
		Addr:    redisHost,
	})
	defer client.Close()

	s := daprd.NewService(":3000")
	s.RegisterActorImplFactoryContext(testActorFactory(client, redisClient))
	log.Println("Started")
	if err := s.Start(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Error listenning: %v", err)
	}
}
