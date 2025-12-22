package errors

import (
	"fmt"
	"net/http"
)

// ErrorCode represents a standardized error code
type ErrorCode string

const (
	// Client errors (4xx)
	ErrCodeValidation      ErrorCode = "VALIDATION_ERROR"
	ErrCodeNotFound        ErrorCode = "NOT_FOUND"
	ErrCodeUnauthorized    ErrorCode = "UNAUTHORIZED"
	ErrCodeForbidden       ErrorCode = "FORBIDDEN"
	ErrCodeDuplicateKey    ErrorCode = "DUPLICATE_KEY"
	ErrCodeConflict        ErrorCode = "CONFLICT"
	ErrCodeBadRequest      ErrorCode = "BAD_REQUEST"
	ErrCodeInvalidInput    ErrorCode = "INVALID_INPUT"
	ErrCodeEntityTooLarge  ErrorCode = "ENTITY_TOO_LARGE"
	ErrCodeRateLimitExceed ErrorCode = "RATE_LIMIT_EXCEEDED"

	// Server errors (5xx)
	ErrCodeDatabaseError  ErrorCode = "DATABASE_ERROR"
	ErrCodeInternalError  ErrorCode = "INTERNAL_ERROR"
	ErrCodeServiceUnavail ErrorCode = "SERVICE_UNAVAILABLE"
	ErrCodeTimeout        ErrorCode = "TIMEOUT"
)

// APIError represents a structured API error with code, message, and optional details
type APIError struct {
	Code       ErrorCode         `json:"code"`
	Message    string            `json:"message"`
	Details    map[string]string `json:"details,omitempty"`
	HTTPStatus int               `json:"-"`
}

// Error implements the error interface
func (e *APIError) Error() string {
	if len(e.Details) > 0 {
		return fmt.Sprintf("%s: %s (details: %v)", e.Code, e.Message, e.Details)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// WithDetails adds details to the error
func (e *APIError) WithDetails(details map[string]string) *APIError {
	e.Details = details
	return e
}

// WithDetail adds a single detail to the error
func (e *APIError) WithDetail(key, value string) *APIError {
	if e.Details == nil {
		e.Details = make(map[string]string)
	}
	e.Details[key] = value
	return e
}

// NewAPIError creates a new APIError with the given code, message, and HTTP status
func NewAPIError(code ErrorCode, message string, httpStatus int) *APIError {
	return &APIError{
		Code:       code,
		Message:    message,
		HTTPStatus: httpStatus,
	}
}

// Validation Error Constructors

// NewValidationError creates a validation error
func NewValidationError(message string) *APIError {
	return &APIError{
		Code:       ErrCodeValidation,
		Message:    message,
		HTTPStatus: http.StatusBadRequest,
	}
}

// NewValidationErrorWithDetails creates a validation error with field-specific details
func NewValidationErrorWithDetails(message string, details map[string]string) *APIError {
	return &APIError{
		Code:       ErrCodeValidation,
		Message:    message,
		Details:    details,
		HTTPStatus: http.StatusBadRequest,
	}
}

// Resource Error Constructors

// NewNotFoundError creates a not found error
func NewNotFoundError(resource string) *APIError {
	return &APIError{
		Code:       ErrCodeNotFound,
		Message:    fmt.Sprintf("%s not found", resource),
		HTTPStatus: http.StatusNotFound,
	}
}

// NewNotFoundErrorWithID creates a not found error with resource ID
func NewNotFoundErrorWithID(resource, id string) *APIError {
	return &APIError{
		Code:    ErrCodeNotFound,
		Message: fmt.Sprintf("%s not found", resource),
		Details: map[string]string{
			"resource": resource,
			"id":       id,
		},
		HTTPStatus: http.StatusNotFound,
	}
}

// Authentication/Authorization Error Constructors

// NewUnauthorizedError creates an unauthorized error
func NewUnauthorizedError(message string) *APIError {
	return &APIError{
		Code:       ErrCodeUnauthorized,
		Message:    message,
		HTTPStatus: http.StatusUnauthorized,
	}
}

// NewForbiddenError creates a forbidden error
func NewForbiddenError(message string) *APIError {
	return &APIError{
		Code:       ErrCodeForbidden,
		Message:    message,
		HTTPStatus: http.StatusForbidden,
	}
}

// Conflict Error Constructors

// NewDuplicateKeyError creates a duplicate key error
func NewDuplicateKeyError(resource, field string) *APIError {
	return &APIError{
		Code:    ErrCodeDuplicateKey,
		Message: fmt.Sprintf("%s already exists", resource),
		Details: map[string]string{
			"resource": resource,
			"field":    field,
		},
		HTTPStatus: http.StatusConflict,
	}
}

// NewConflictError creates a generic conflict error
func NewConflictError(message string) *APIError {
	return &APIError{
		Code:       ErrCodeConflict,
		Message:    message,
		HTTPStatus: http.StatusConflict,
	}
}

// Request Error Constructors

// NewBadRequestError creates a bad request error
func NewBadRequestError(message string) *APIError {
	return &APIError{
		Code:       ErrCodeBadRequest,
		Message:    message,
		HTTPStatus: http.StatusBadRequest,
	}
}

// NewInvalidInputError creates an invalid input error
func NewInvalidInputError(field, message string) *APIError {
	return &APIError{
		Code:    ErrCodeInvalidInput,
		Message: "Invalid input",
		Details: map[string]string{
			"field":  field,
			"reason": message,
		},
		HTTPStatus: http.StatusBadRequest,
	}
}

// NewEntityTooLargeError creates an entity too large error
func NewEntityTooLargeError(message string) *APIError {
	return &APIError{
		Code:       ErrCodeEntityTooLarge,
		Message:    message,
		HTTPStatus: http.StatusRequestEntityTooLarge,
	}
}

// NewRateLimitError creates a rate limit exceeded error
func NewRateLimitError(message string) *APIError {
	return &APIError{
		Code:       ErrCodeRateLimitExceed,
		Message:    message,
		HTTPStatus: http.StatusTooManyRequests,
	}
}

// Server Error Constructors

// NewDatabaseError creates a database error
func NewDatabaseError(operation string) *APIError {
	return &APIError{
		Code:    ErrCodeDatabaseError,
		Message: "Database operation failed",
		Details: map[string]string{
			"operation": operation,
		},
		HTTPStatus: http.StatusInternalServerError,
	}
}

// NewInternalError creates an internal server error
func NewInternalError(message string) *APIError {
	if message == "" {
		message = "An internal error occurred"
	}
	return &APIError{
		Code:       ErrCodeInternalError,
		Message:    message,
		HTTPStatus: http.StatusInternalServerError,
	}
}

// NewServiceUnavailableError creates a service unavailable error
func NewServiceUnavailableError(service string) *APIError {
	return &APIError{
		Code:    ErrCodeServiceUnavail,
		Message: "Service temporarily unavailable",
		Details: map[string]string{
			"service": service,
		},
		HTTPStatus: http.StatusServiceUnavailable,
	}
}

// NewTimeoutError creates a timeout error
func NewTimeoutError(operation string) *APIError {
	return &APIError{
		Code:    ErrCodeTimeout,
		Message: "Operation timed out",
		Details: map[string]string{
			"operation": operation,
		},
		HTTPStatus: http.StatusGatewayTimeout,
	}
}

// Helper functions

// IsNotFoundError checks if an error is a not found error
func IsNotFoundError(err error) bool {
	apiErr, ok := err.(*APIError)
	return ok && apiErr.Code == ErrCodeNotFound
}

// IsDatabaseError checks if an error is a database error
func IsDatabaseError(err error) bool {
	apiErr, ok := err.(*APIError)
	return ok && apiErr.Code == ErrCodeDatabaseError
}

// IsValidationError checks if an error is a validation error
func IsValidationError(err error) bool {
	apiErr, ok := err.(*APIError)
	return ok && apiErr.Code == ErrCodeValidation
}

// IsUnauthorizedError checks if an error is an unauthorized error
func IsUnauthorizedError(err error) bool {
	apiErr, ok := err.(*APIError)
	return ok && apiErr.Code == ErrCodeUnauthorized
}

// WrapError wraps a standard error into an APIError
// If the error is already an APIError, it returns it as-is
func WrapError(err error, message string) *APIError {
	if apiErr, ok := err.(*APIError); ok {
		return apiErr
	}
	return NewInternalError(message).WithDetail("original_error", err.Error())
}
