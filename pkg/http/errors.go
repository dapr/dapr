package http

// ErrorResponse is an HTTP response message sent back to calling clients by the Actions Runtime HTTP API
type ErrorResponse struct {
	ErrorCode string `json:"errorCode"`
	Message   string `json:"message"`
}

// NewErrorResponse returns a new ErrorResponse
func NewErrorResponse(errorCode, message string) ErrorResponse {
	return ErrorResponse{
		ErrorCode: errorCode,
		Message:   message,
	}
}
