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

package messages

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"

	grpcCodes "google.golang.org/grpc/codes"
	grpcStatus "google.golang.org/grpc/status"
)

func TestAPIError_WithFormat(t *testing.T) {
	type fields struct {
		message  string
		tag      string
		httpCode int
		grpcCode grpcCodes.Code
	}
	type args struct {
		a []any
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   APIError
	}{
		{
			name:   "no formatting",
			fields: fields{message: "ciao", tag: "MYERR", httpCode: http.StatusTeapot, grpcCode: grpcCodes.ResourceExhausted},
			args:   args{a: []any{}},
			want:   APIError{message: "ciao", tag: "MYERR", httpCode: http.StatusTeapot, grpcCode: grpcCodes.ResourceExhausted},
		},
		{
			name:   "string parameter",
			fields: fields{message: "ciao %s", tag: "MYERR", httpCode: http.StatusTeapot, grpcCode: grpcCodes.ResourceExhausted},
			args:   args{a: []any{"mondo"}},
			want:   APIError{message: "ciao mondo", tag: "MYERR", httpCode: http.StatusTeapot, grpcCode: grpcCodes.ResourceExhausted},
		},
		{
			name:   "multiple params",
			fields: fields{message: "ciao %s %d", tag: "MYERR", httpCode: http.StatusTeapot, grpcCode: grpcCodes.ResourceExhausted},
			args:   args{a: []any{"mondo", 42}},
			want:   APIError{message: "ciao mondo 42", tag: "MYERR", httpCode: http.StatusTeapot, grpcCode: grpcCodes.ResourceExhausted},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := APIError{
				message:  tt.fields.message,
				tag:      tt.fields.tag,
				httpCode: tt.fields.httpCode,
				grpcCode: tt.fields.grpcCode,
			}
			if got := e.WithFormat(tt.args.a...); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("APIError.WithFormat() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAPIError_Message(t *testing.T) {
	type fields struct {
		message string
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{
			name:   "has message",
			fields: fields{message: "Cantami, o Diva, del Pelide Achille"},
			want:   "Cantami, o Diva, del Pelide Achille",
		},
		{
			name:   "no message",
			fields: fields{message: ""},
			want:   defaultMessage,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := APIError{
				message: tt.fields.message,
			}
			if got := e.Message(); got != tt.want {
				t.Errorf("APIError.Message() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAPIError_Tag(t *testing.T) {
	type fields struct {
		tag string
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{
			name:   "has tag",
			fields: fields{tag: "SOME_ERROR"},
			want:   "SOME_ERROR",
		},
		{
			name:   "no tag",
			fields: fields{tag: ""},
			want:   defaultTag,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := APIError{
				tag: tt.fields.tag,
			}
			if got := e.Tag(); got != tt.want {
				t.Errorf("APIError.Tag() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAPIError_HTTPCode(t *testing.T) {
	type fields struct {
		httpCode int
	}
	tests := []struct {
		name   string
		fields fields
		want   int
	}{
		{
			name:   "has httpCode",
			fields: fields{httpCode: http.StatusTeapot},
			want:   http.StatusTeapot,
		},
		{
			name:   "no httpCode",
			fields: fields{httpCode: 0},
			want:   http.StatusInternalServerError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := APIError{
				httpCode: tt.fields.httpCode,
			}
			if got := e.HTTPCode(); got != tt.want {
				t.Errorf("APIError.HTTPCode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAPIError_GRPCStatus(t *testing.T) {
	type fields struct {
		message  string
		grpcCode grpcCodes.Code
	}
	tests := []struct {
		name   string
		fields fields
		want   *grpcStatus.Status
	}{
		{
			name: "has grpcCode and message",
			fields: fields{
				message:  "Oy vey",
				grpcCode: grpcCodes.ResourceExhausted,
			},
			want: grpcStatus.New(grpcCodes.ResourceExhausted, "Oy vey"),
		},
		{
			name: "has only message",
			fields: fields{
				message: "Oy vey",
			},
			// The default code is 0, i.e. OK
			want: grpcStatus.New(grpcCodes.OK, "Oy vey"),
		},
		{
			name: "has only grpcCode",
			fields: fields{
				grpcCode: grpcCodes.Canceled,
			},
			want: grpcStatus.New(grpcCodes.Canceled, defaultMessage),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := APIError{
				message:  tt.fields.message,
				grpcCode: tt.fields.grpcCode,
			}
			if got := e.GRPCStatus(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("APIError.GRPCStatus() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAPIError_Error(t *testing.T) {
	type fields struct {
		message  string
		grpcCode grpcCodes.Code
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{
			name: "has grpcCode and message",
			fields: fields{
				message:  "Oy vey",
				grpcCode: grpcCodes.ResourceExhausted,
			},
			want: fmt.Sprintf(errStringFormat, grpcCodes.ResourceExhausted, "Oy vey"),
		},
		{
			name: "has only message",
			fields: fields{
				message: "Oy vey",
			},
			// The default code is 0, i.e. OK
			want: fmt.Sprintf(errStringFormat, grpcCodes.OK, "Oy vey"),
		},
		{
			name: "has only grpcCode",
			fields: fields{
				grpcCode: grpcCodes.Canceled,
			},
			want: fmt.Sprintf(errStringFormat, grpcCodes.Canceled, defaultMessage),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := APIError{
				message:  tt.fields.message,
				grpcCode: tt.fields.grpcCode,
			}
			if got := e.Error(); got != tt.want {
				t.Errorf("APIError.Error() = %v, want %v", got, tt.want)
			}
		})
	}
}
