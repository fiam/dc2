package api

import (
	"errors"
	"fmt"
)

const (
	ErrorCodeInvalidAction    = "InvalidAction"
	ErrorCodeInstanceNotFound = "InvalidInstanceID.NotFound"
	ErrorCodeDryRunOperation  = "DryRunOperation"

	// Custom errors
	ErrorCodeMethodNotAllowed = "MethodNotAllowed"
	ErrorCodeInvalidForm      = "InvalidForm"
)

type Error struct {
	Code string
	Err  error
}

func (e *Error) Unwrap() error {
	return e.Err
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s", e.Code, e.Err.Error())
	}
	return e.Code
}

func ErrWithCode(code string, err error) *Error {
	return &Error{
		Code: code,
		Err:  err,
	}
}

func DryRunError() *Error {
	//nolint
	err := errors.New("Request would have succeeded, but DryRun flag is set.")
	return ErrWithCode(ErrorCodeDryRunOperation, err)
}
