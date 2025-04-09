/*
Copyright 2025 The Dapr Authors
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

package filter

import (
	"errors"
	"fmt"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

// FilterFunc takes a CloudEvent and returns true if the event matches the
// filter.
type FilterFunc func(map[string]any) bool

func New(filters []*rtv1.SubscribeActorEventRequestFilterAlpha1) (FilterFunc, error) {
	if len(filters) == 0 {
		return func(map[string]any) bool { return true }, nil
	}

	fn, err := parseFilters(filters, filterAnd)
	if err != nil {
		return nil, fmt.Errorf("failed to parse actor subscription filters: %w", err)
	}

	return fn, nil
}

func parse(filter *rtv1.SubscribeActorEventRequestFilterAlpha1) (FilterFunc, error) {
	switch ff := filter.Filter.(type) {
	case *rtv1.SubscribeActorEventRequestFilterAlpha1_Exact:
		return parseExpressions(ff.Exact.GetExpression(), opExact)
	case *rtv1.SubscribeActorEventRequestFilterAlpha1_Prefix:
		return parseExpressions(ff.Prefix.GetExpression(), opPrefix)
	case *rtv1.SubscribeActorEventRequestFilterAlpha1_Suffix:
		return parseExpressions(ff.Suffix.GetExpression(), opSuffix)

	case *rtv1.SubscribeActorEventRequestFilterAlpha1_All:
		return parseFilters(ff.All.GetFilters(), filterAnd)
	case *rtv1.SubscribeActorEventRequestFilterAlpha1_Any:
		return parseFilters(ff.Any.GetFilters(), filterOr)
	case *rtv1.SubscribeActorEventRequestFilterAlpha1_None:
		return parseNot(ff.None.GetFilters())

	default:
		return nil, fmt.Errorf("unknown filter type: %T", filter)
	}
}

func parseFilters(filters []*rtv1.SubscribeActorEventRequestFilterAlpha1, op filterOp) (FilterFunc, error) {
	if len(filters) == 0 {
		return nil, errors.New("no filters provided")
	}

	fn, err := parse(filters[0])
	if err != nil {
		return nil, err
	}

	for _, filter := range filters {
		fnx, err := parse(filter)
		if err != nil {
			return nil, err
		}
		fn = makeFilter(fn, fnx, op)
	}

	return fn, nil
}

func parseNot(filters []*rtv1.SubscribeActorEventRequestFilterAlpha1) (FilterFunc, error) {
	if len(filters) != 1 {
		return nil, errors.New("'not' filter must have exactly one filter")
	}

	fn, err := parse(filters[0])
	if err != nil {
		return nil, err
	}

	return filterNot(fn), nil
}

func parseExpressions(exps []*rtv1.SubscribeActorEventRequestExpressionAlpha1, op expressionOp) (FilterFunc, error) {
	if len(exps) == 0 {
		return nil, errors.New("no expressions provided")
	}

	fn, err := parseExpression(exps[0], op)
	if err != nil {
		return nil, err
	}

	for _, exp := range exps[1:] {
		fnx, err := parseExpression(exp, op)
		if err != nil {
			return nil, err
		}
		fn = makeFilter(fn, fnx, filterAnd)
	}

	return fn, nil
}

func parseExpression(exp *rtv1.SubscribeActorEventRequestExpressionAlpha1, op expressionOp) (FilterFunc, error) {
	if len(exp.GetAttributePath()) == 0 {
		return nil, errors.New("expression attribute path is empty")
	}

	for _, c := range exp.GetAttributePath() {
		if len(c) == 0 {
			return nil, errors.New("expression attribute path contains empty component")
		}
	}

	if len(exp.GetValue()) == 0 {
		return nil, errors.New("expression value is empty")
	}

	return makeOp(exp.GetAttributePath(), exp.GetValue(), op), nil
}
