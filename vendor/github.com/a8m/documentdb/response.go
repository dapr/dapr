package documentdb

import (
	"math"
	"net/http"
)

type Response struct {
	Header http.Header
}

// Continuation returns continuation token for paged request.
// Pass this value to next request to get next page of documents.
func (r *Response) Continuation() string {
	return r.Header.Get(HeaderContinuation)
}

type statusCodeValidatorFunc func(statusCode int) bool

func expectStatusCode(expected int) statusCodeValidatorFunc {
	return func(statusCode int) bool {
		return expected == statusCode
	}
}

func expectStatusCodeXX(expected int) statusCodeValidatorFunc {
	begining := int(math.Floor(float64(expected/100))) * 100
	end := begining + 99
	return func(statusCode int) bool {
		return (statusCode >= begining) && (statusCode <= end)
	}
}
