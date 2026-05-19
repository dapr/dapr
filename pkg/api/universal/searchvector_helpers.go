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
	compsearch "github.com/dapr/components-contrib/search"
	"google.golang.org/protobuf/types/known/structpb"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func protoStructToMap(s *structpb.Struct) map[string]any {
	if s == nil {
		return nil
	}
	return s.AsMap()
}

func mapToProtoStruct(m map[string]any) (*structpb.Struct, error) {
	if m == nil {
		return nil, nil
	}
	return structpb.NewStruct(m)
}

func protoIndexFieldsToComponent(fields []*runtimev1pb.IndexFieldSchema) []compsearch.IndexFieldSchema {
	if len(fields) == 0 {
		return nil
	}

	out := make([]compsearch.IndexFieldSchema, 0, len(fields))
	for _, field := range fields {
		out = append(out, compsearch.IndexFieldSchema{
			Name:       field.GetName(),
			Type:       compsearch.IndexFieldType(int32(field.GetType())),
			Filterable: field.GetFilterable(),
			Sortable:   field.GetSortable(),
			Searchable: field.GetSearchable(),
		})
	}
	return out
}

func componentIndexFieldsToProto(fields []compsearch.IndexFieldSchema) []*runtimev1pb.IndexFieldSchema {
	if len(fields) == 0 {
		return nil
	}

	out := make([]*runtimev1pb.IndexFieldSchema, 0, len(fields))
	for _, field := range fields {
		out = append(out, &runtimev1pb.IndexFieldSchema{
			Name:       field.Name,
			Type:       runtimev1pb.IndexFieldType(field.Type),
			Filterable: field.Filterable,
			Sortable:   field.Sortable,
			Searchable: field.Searchable,
		})
	}
	return out
}

func componentOperationResultsToProto(results []compsearch.OperationResult) []*runtimev1pb.OperationResult {
	if len(results) == 0 {
		return nil
	}

	out := make([]*runtimev1pb.OperationResult, 0, len(results))
	for _, result := range results {
		out = append(out, &runtimev1pb.OperationResult{
			Id:           result.ID,
			Success:      result.Success,
			ErrorCode:    result.ErrorCode,
			ErrorMessage: result.ErrorMessage,
		})
	}
	return out
}
