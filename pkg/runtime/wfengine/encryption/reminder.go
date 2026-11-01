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

package encryption

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

const nonceSize = 12

// ReminderAAD binds a sealed reminder to the actor and reminder it was
// created for.
func ReminderAAD(actorType, actorID, reminderName string) string {
	return actorType + "||" + actorID + "||" + reminderName
}

// IsSealedReminder reports whether reminder data is an EncryptedPayload.
func IsSealedReminder(data *anypb.Any) bool {
	return data.MessageIs((*internalsv1pb.EncryptedPayload)(nil))
}

// SealReminder seals reminder data with the workflow instance's key. The
// wrapped key travels with it, so the receiver needs no state of the sender
// to open it. A nil key, for an instance stored in plaintext, returns the data
// as is.
func SealReminder(k *InstanceKey, aad string, inner *anypb.Any) (*anypb.Any, error) {
	if k == nil || inner == nil {
		return inner, nil
	}
	b, err := proto.Marshal(inner)
	if err != nil {
		return nil, err
	}
	sealed := k.Seal(aad, b)
	return anypb.New(&internalsv1pb.EncryptedPayload{
		Key:        k.Wrapped(),
		Nonce:      sealed[:nonceSize],
		Ciphertext: sealed[nonceSize:],
	})
}

// OpenReminder returns the inner data of a sealed reminder and the key it
// was sealed with. Data that is not sealed is returned as is with a nil key.
// A nil Encryptor rejects sealed data.
func (e *Encryptor) OpenReminder(ctx context.Context, aad string, data *anypb.Any) (*anypb.Any, *InstanceKey, error) {
	if !IsSealedReminder(data) {
		return data, nil, nil
	}
	if e == nil {
		return nil, nil, errors.New("reminder payload is encrypted but workflow payload encryption is not configured")
	}

	var ep internalsv1pb.EncryptedPayload
	if err := data.UnmarshalTo(&ep); err != nil {
		return nil, nil, fmt.Errorf("malformed encrypted reminder payload: %w", err)
	}
	k, err := e.Open(ctx, ep.GetKey())
	if err != nil {
		return nil, nil, err
	}
	b, err := k.Unseal(aad, slices.Concat(ep.GetNonce(), ep.GetCiphertext()))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decrypt reminder payload: %w", err)
	}
	var inner anypb.Any
	if err := proto.Unmarshal(b, &inner); err != nil {
		return nil, nil, fmt.Errorf("malformed decrypted reminder payload: %w", err)
	}
	return &inner, k, nil
}
