package response

import (
	"zohoclient/internal/lib/clock"
	apierrors "zohoclient/internal/lib/errors"
)

type Response struct {
	Data          interface{}  `json:"data,omitempty"`
	Success       bool         `json:"success" validate:"required"`
	StatusMessage string       `json:"status_message"`
	Timestamp     string       `json:"timestamp"`
	Pagination    *Pagination  `json:"pagination,omitempty"`
	Error         *ErrorDetail `json:"error,omitempty"`
	RequestID     string       `json:"request_id,omitempty"`
}

type Pagination struct {
	Page       int `json:"page"`
	Count      int `json:"count"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
}

// ErrorDetail provides structured error information in responses
type ErrorDetail struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

func Ok(data interface{}) Response {
	return Response{
		Data:          data,
		Success:       true,
		StatusMessage: "Success",
		Timestamp:     clock.Now(),
	}
}

func OkWithMessage(data interface{}, message string) Response {
	return Response{
		Data:          data,
		Success:       true,
		StatusMessage: message,
		Timestamp:     clock.Now(),
	}
}

func OkWithPagination(data interface{}, page, count, total int) Response {
	totalPages := 0
	if count > 0 {
		totalPages = (total + count - 1) / count // Ceiling division
	}
	return Response{
		Data:          data,
		Success:       true,
		StatusMessage: "Success",
		Timestamp:     clock.Now(),
		Pagination: &Pagination{
			Page:       page,
			Count:      count,
			Total:      total,
			TotalPages: totalPages,
		},
	}
}

// Error creates an error response with a simple message
// Deprecated: Use ErrorWithCode or ErrorFromAPIError for structured errors
func Error(message string) Response {
	return Response{
		Success:       false,
		StatusMessage: message,
		Timestamp:     clock.Now(),
	}
}

// ErrorWithCode creates an error response with a code and message
func ErrorWithCode(code, message string) Response {
	return Response{
		Success:       false,
		StatusMessage: message,
		Timestamp:     clock.Now(),
		Error: &ErrorDetail{
			Code:    code,
			Message: message,
		},
	}
}

// ErrorWithDetails creates an error response with code, message, and details
func ErrorWithDetails(code, message string, details map[string]string) Response {
	return Response{
		Success:       false,
		StatusMessage: message,
		Timestamp:     clock.Now(),
		Error: &ErrorDetail{
			Code:    code,
			Message: message,
			Details: details,
		},
	}
}

// ErrorFromAPIError creates a response from an APIError
func ErrorFromAPIError(err *apierrors.APIError) Response {
	errorDetail := &ErrorDetail{
		Code:    string(err.Code),
		Message: err.Message,
		Details: err.Details,
	}

	return Response{
		Success:       false,
		StatusMessage: err.Message,
		Timestamp:     clock.Now(),
		Error:         errorDetail,
	}
}

// WithRequestID adds a request ID to the response
func (r Response) WithRequestID(requestID string) Response {
	r.RequestID = requestID
	return r
}
