package workflow

import (
	"encoding/gob"
	"errors"
	"strings"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// runFailure is the failure of a step or a run in the form DBOS can store: it
// encodes errors with gob, which cannot encode an apperr error, and keeps only
// the message of an error it cannot encode, so another process would read
// every failure as Internal.
type runFailure struct {
	// Code and Message are the stored apperr code and error text.
	Code    codes.Code
	Message string
	// cause is the original error. It is set only in the process that failed,
	// so errors.Is still finds what the error wrapped.
	cause error
}

func init() {
	// DBOS decodes a stored error in every process that reads one.
	gob.Register(&runFailure{})
}

// Error implements error.
func (f *runFailure) Error() string { return f.Message }

// Unwrap returns the original error when this process has it, else an apperr
// error with the stored code and message.
func (f *runFailure) Unwrap() error {
	if f.cause != nil {
		return f.cause
	}
	return apperr.New(f.Code, strings.TrimSuffix(f.Message, " ("+f.Code.String()+")"))
}

// storable returns err in the form that keeps its apperr code once stored; an
// error without a code is Internal. DBOS's own errors are returned unchanged,
// because DBOS encodes and handles them itself.
func storable(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*dbos.Error](err); ok {
		return err
	}
	if f, ok := err.(*runFailure); ok {
		return f
	}
	code := codes.Internal
	if appErr, ok := errors.AsType[*apperr.AppErr](err); ok {
		code = appErr.Code
	}
	return &runFailure{Code: code, Message: err.Error(), cause: err}
}
