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

package universal

import (
	"context"
	"io"

	"google.golang.org/grpc"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

// IndexDocumentsHTTPAlpha1 invokes the client-streaming IndexDocumentsAlpha1 API with a single request.
func (a *Universal) IndexDocumentsHTTPAlpha1(ctx context.Context, req *runtimev1pb.IndexDocumentsRequestAlpha1) (*runtimev1pb.IndexDocumentsResponseAlpha1, error) {
	stream := &indexDocumentsHTTPAlpha1Server{ctx: ctx, req: req}
	if err := a.IndexDocumentsAlpha1(stream); err != nil {
		return nil, err
	}
	return stream.resp, nil
}

// SearchHTTPAlpha1 invokes the server-streaming SearchAlpha1 API and returns a single aggregated response.
func (a *Universal) SearchHTTPAlpha1(ctx context.Context, req *runtimev1pb.SearchRequestAlpha1) (*runtimev1pb.SearchResponseAlpha1, error) {
	stream := &searchHTTPAlpha1Server{ctx: ctx}
	if err := a.SearchAlpha1(req, stream); err != nil {
		return nil, err
	}
	resp := &runtimev1pb.SearchResponseAlpha1{}
	for _, chunk := range stream.responses {
		resp.Hits = append(resp.Hits, chunk.GetHits()...)
		resp.TotalHits = chunk.GetTotalHits()
		resp.ContinuationToken = chunk.GetContinuationToken()
	}
	return resp, nil
}

// UpsertVectorsHTTPAlpha1 invokes the client-streaming UpsertVectorsAlpha1 API with a single request.
func (a *Universal) UpsertVectorsHTTPAlpha1(ctx context.Context, req *runtimev1pb.UpsertVectorsRequestAlpha1) (*runtimev1pb.UpsertVectorsResponseAlpha1, error) {
	stream := &upsertVectorsHTTPAlpha1Server{ctx: ctx, req: req}
	if err := a.UpsertVectorsAlpha1(stream); err != nil {
		return nil, err
	}
	return stream.resp, nil
}

// QueryVectorsHTTPAlpha1 invokes the server-streaming QueryVectorsAlpha1 API and returns a single aggregated response.
func (a *Universal) QueryVectorsHTTPAlpha1(ctx context.Context, req *runtimev1pb.QueryVectorsRequestAlpha1) (*runtimev1pb.QueryVectorsResponseAlpha1, error) {
	stream := &queryVectorsHTTPAlpha1Server{ctx: ctx}
	if err := a.QueryVectorsAlpha1(req, stream); err != nil {
		return nil, err
	}
	resp := &runtimev1pb.QueryVectorsResponseAlpha1{}
	for _, chunk := range stream.responses {
		resp.Hits = append(resp.Hits, chunk.GetHits()...)
		resp.ContinuationToken = chunk.GetContinuationToken()
	}
	return resp, nil
}

type indexDocumentsHTTPAlpha1Server struct {
	grpc.ServerStream
	ctx  context.Context
	req  *runtimev1pb.IndexDocumentsRequestAlpha1
	resp *runtimev1pb.IndexDocumentsResponseAlpha1
	sent bool
}

func (s *indexDocumentsHTTPAlpha1Server) Context() context.Context {
	return s.ctx
}

func (s *indexDocumentsHTTPAlpha1Server) Recv() (*runtimev1pb.IndexDocumentsRequestAlpha1, error) {
	if s.sent {
		return nil, io.EOF
	}
	s.sent = true
	return s.req, nil
}

func (s *indexDocumentsHTTPAlpha1Server) SendAndClose(resp *runtimev1pb.IndexDocumentsResponseAlpha1) error {
	s.resp = resp
	return nil
}

type searchHTTPAlpha1Server struct {
	grpc.ServerStream
	ctx       context.Context
	responses []*runtimev1pb.SearchResponseAlpha1
}

func (s *searchHTTPAlpha1Server) Context() context.Context {
	return s.ctx
}

func (s *searchHTTPAlpha1Server) Send(resp *runtimev1pb.SearchResponseAlpha1) error {
	s.responses = append(s.responses, resp)
	return nil
}

type upsertVectorsHTTPAlpha1Server struct {
	grpc.ServerStream
	ctx  context.Context
	req  *runtimev1pb.UpsertVectorsRequestAlpha1
	resp *runtimev1pb.UpsertVectorsResponseAlpha1
	sent bool
}

func (s *upsertVectorsHTTPAlpha1Server) Context() context.Context {
	return s.ctx
}

func (s *upsertVectorsHTTPAlpha1Server) Recv() (*runtimev1pb.UpsertVectorsRequestAlpha1, error) {
	if s.sent {
		return nil, io.EOF
	}
	s.sent = true
	return s.req, nil
}

func (s *upsertVectorsHTTPAlpha1Server) SendAndClose(resp *runtimev1pb.UpsertVectorsResponseAlpha1) error {
	s.resp = resp
	return nil
}

type queryVectorsHTTPAlpha1Server struct {
	grpc.ServerStream
	ctx       context.Context
	responses []*runtimev1pb.QueryVectorsResponseAlpha1
}

func (s *queryVectorsHTTPAlpha1Server) Context() context.Context {
	return s.ctx
}

func (s *queryVectorsHTTPAlpha1Server) Send(resp *runtimev1pb.QueryVectorsResponseAlpha1) error {
	s.responses = append(s.responses, resp)
	return nil
}
