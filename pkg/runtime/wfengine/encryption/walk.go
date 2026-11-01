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
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/durabletask-go/api/protos"
)

var (
	stringValueName    = (&wrapperspb.StringValue{}).ProtoReflect().Descriptor().FullName()
	failureDetailsName = (&protos.TaskFailureDetails{}).ProtoReflect().Descriptor().FullName()
	chunkName          = (&protos.PropagatedHistoryChunk{}).ProtoReflect().Descriptor().FullName()
)

type fieldClass int

const (
	plaintext fieldClass = iota
	sealedStringValue
	sealedString
	sealedBytesList
	descend
)

// classify decides how a field of a persisted workflow message is stored.
// The payloads are the `input` and `result` StringValues of history events
// and the error message and stack trace of failure details. A propagated
// history chunk carries other events' raw bytes, which are sealed whole.
// Everything else (names, IDs, versions, timestamps, routing, trace context,
// error types, signatures) stays readable.
func classify(parent protoreflect.FullName, fd protoreflect.FieldDescriptor) fieldClass {
	switch {
	case parent == failureDetailsName && fd.Name() == "errorMessage":
		return sealedString
	case parent == failureDetailsName && fd.Name() == "stackTrace":
		return sealedStringValue
	case parent == chunkName && fd.Name() == "rawEvents":
		return sealedBytesList
	case fd.Message() != nil && fd.Message().FullName() == stringValueName:
		if fd.Name() == "input" || fd.Name() == "result" {
			return sealedStringValue
		}
		return plaintext
	case fd.Message() != nil && !fd.IsMap():
		return descend
	default:
		return plaintext
	}
}

// SealMessage returns a copy of m with its payload fields sealed. Every
// sealed value is bound to aadPrefix and to its field path, so it cannot be
// moved to another row or field.
func SealMessage[T proto.Message](k *InstanceKey, aadPrefix string, m T) (T, error) {
	c := proto.Clone(m).(T)
	err := codec{
		str: func(aad, s string) (string, error) { return k.SealString(aad, s), nil },
		raw: func(aad string, b []byte) ([]byte, error) { return k.Seal(aad, b), nil },
	}.walk(c.ProtoReflect(), aadPrefix, "")
	return c, err
}

// UnsealMessage unseals m's payload fields in place. Every populated payload
// field must be sealed under aadPrefix.
func UnsealMessage(k *InstanceKey, aadPrefix string, m proto.Message) error {
	return codec{str: k.UnsealString, raw: k.Unseal}.walk(m.ProtoReflect(), aadPrefix, "")
}

type codec struct {
	str func(aad, s string) (string, error)
	raw func(aad string, b []byte) ([]byte, error)
}

func (c codec) walk(m protoreflect.Message, prefix, path string) error {
	var err error
	parent := m.Descriptor().FullName()
	m.Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		p := string(fd.Name())
		if path != "" {
			p = path + "." + p
		}

		switch classify(parent, fd) {
		case sealedStringValue:
			sv := m.Mutable(fd).Message().Interface().(*wrapperspb.StringValue)
			var s string
			if s, err = c.str(prefix+"||"+p, sv.GetValue()); err != nil {
				err = fmt.Errorf("field %s: %w", p, err)
			} else {
				sv.Value = s
			}

		case sealedString:
			var s string
			if s, err = c.str(prefix+"||"+p, m.Get(fd).String()); err != nil {
				err = fmt.Errorf("field %s: %w", p, err)
			} else {
				m.Set(fd, protoreflect.ValueOfString(s))
			}

		case sealedBytesList:
			l := m.Mutable(fd).List()
			for i := range l.Len() {
				ip := fmt.Sprintf("%s[%d]", p, i)
				var b []byte
				if b, err = c.raw(prefix+"||"+ip, l.Get(i).Bytes()); err != nil {
					err = fmt.Errorf("field %s: %w", ip, err)
					break
				}
				l.Set(i, protoreflect.ValueOfBytes(b))
			}

		case descend:
			if fd.IsList() {
				l := m.Mutable(fd).List()
				for i := range l.Len() {
					if err = c.walk(l.Get(i).Message(), prefix, fmt.Sprintf("%s[%d]", p, i)); err != nil {
						break
					}
				}
			} else {
				err = c.walk(m.Mutable(fd).Message(), prefix, p)
			}

		case plaintext:
		}

		return err == nil
	})
	return err
}
