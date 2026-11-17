package apperr

import "fmt"

type Error struct {
	Code   string `json:"code"`
	Message string `json:"message"`
	Status int    `json:"-"`
}

func (e *Error) Error() string { return e.Message }

func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func BadRequest(code, message string) *Error { return New(400, code, message) }
func Unauthorized(message string) *Error     { return New(401, "UNAUTHORIZED", message) }
func Forbidden(message string) *Error        { return New(403, "FORBIDDEN", message) }
func NotFound(message string) *Error         { return New(404, "NOT_FOUND", message) }
func Conflict(code, message string) *Error   { return New(409, code, message) }
func TooLarge(message string) *Error         { return New(413, "FILE_TOO_LARGE", message) }
func Internal(message string) *Error         { return New(500, "INTERNAL", message) }

func Wrap(err error) *Error {
	if err == nil {
		return nil
	}
	if e, ok := err.(*Error); ok {
		return e
	}
	return New(500, "INTERNAL", fmt.Sprintf("服务器内部错误：%v", err))
}
