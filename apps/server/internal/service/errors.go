package service

// AppError is an error with an HTTP status code for consistent error handling
// across HTTP and MCP transports.
type AppError struct {
	Status  int
	Message string
}

func (e *AppError) Error() string { return e.Message }

func ErrBadRequest(msg string) *AppError { return &AppError{400, msg} }
func ErrForbidden(msg string) *AppError  { return &AppError{403, msg} }
func ErrNotFound(msg string) *AppError   { return &AppError{404, msg} }
func ErrConflict(msg string) *AppError   { return &AppError{409, msg} }
func ErrRateLimit(msg string) *AppError  { return &AppError{429, msg} }
func ErrInternal(msg string) *AppError   { return &AppError{500, msg} }
