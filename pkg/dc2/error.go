package dc2

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

type AWSError struct {
	Code string
	Err  error
}

func (e *AWSError) Unwrap() error {
	return e.Err
}

func (e *AWSError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s", e.Code, e.Err.Error())
	}
	return e.Code
}

func ErrWithCode(code string, err error) *AWSError {
	return &AWSError{
		Code: code,
		Err:  err,
	}
}

func DryRunError() *AWSError {
	return ErrWithCode(ErrorCodeDryRunOperation, errors.New("Request would have succeeded, but DryRun flag is set."))
}
