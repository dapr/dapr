/*
Copyright 2026 The Dapr Authors
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

import "testing"

func TestExtractMessage(t *testing.T) {
	const cloudEventID = "123e4567-e89b-12d3-a456-426614174000"

	tests := map[string]struct {
		body        string
		wantMessage string
		wantID      string
		wantErr     bool
	}{
		"base64 payload retains CloudEvent ID": {
			body:        `{"id":"123e4567-e89b-12d3-a456-426614174000","data_base64":"aGVsbG8="}`,
			wantMessage: "hello",
			wantID:      cloudEventID,
		},
		"non-string id returns error": {
			body:    `{"id":123,"data":"hello","pubsubname":"messagebus"}`,
			wantErr: true,
		},
		"non-string base64 data returns error": {
			body:    `{"id":"123e4567-e89b-12d3-a456-426614174000","data_base64":123}`,
			wantErr: true,
		},
		"non-string data returns error": {
			body:    `{"id":"123e4567-e89b-12d3-a456-426614174000","data":123,"pubsubname":"messagebus"}`,
			wantErr: true,
		},
		"non-string pubsub name returns error": {
			body:    `{"id":"123e4567-e89b-12d3-a456-426614174000","data":"hello","pubsubname":123}`,
			wantErr: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			message, id, err := extractMessage("test", []byte(test.body))
			if test.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}

			if err != nil {
				t.Fatalf("extractMessage() error = %v", err)
			}
			if message != test.wantMessage {
				t.Errorf("message = %q, want %q", message, test.wantMessage)
			}
			if id != test.wantID {
				t.Errorf("CloudEvent ID = %q, want %q", id, test.wantID)
			}
		})
	}
}
