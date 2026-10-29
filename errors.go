package qngg

import (
	"errors"
	"fmt"
	"reflect"
)

var (
	// ErrRuntimeClosed is returned when a closed runtime, or a value that
	// belongs to one, is used.
	ErrRuntimeClosed = errors.New("runtime is closed")
	// ErrInterrupted is returned when the context of a call is done while
	// JavaScript is running. The runtime is left in an undefined state:
	// every later call fails with this error, and it must be closed.
	ErrInterrupted = errors.New("javascript interrupted")
	// ErrFreed is returned when a value is used after Free.
	ErrFreed = errors.New("value is freed")
	// ErrInvalidFileName is returned when an empty name is evaluated.
	ErrInvalidFileName = errors.New("file name is required")

	errRType                 = reflect.TypeFor[error]()
	errZeroRValue            = reflect.Zero(errRType)
	errCallFuncOnNonObject   = errors.New("cannot call function on non-object")
	errObjectNotAConstructor = errors.New("object not a constructor")
	errIndexOutOfRange       = errors.New("index out of range")
	errEmptyStringToNumber   = errors.New("empty string cannot be converted to number")
	errNotByteArray          = errors.New("invalid TypedArray: buffer is not a byte array")
	errNotArrayBuffer        = errors.New("input is not an ArrayBuffer")
	errMissingBufferProperty = errors.New("invalid TypedArray: missing buffer property")
	errChanClosed            = errors.New("channel is closed")
	errChanSend              = errors.New("channel send would block: buffer full or no receiver ready")
	errChanReceive           = errors.New("channel receive would block: buffer empty or no sender ready")
	errChanCloseReceiveOnly  = errors.New("cannot close receive-only channel")
)

// Error is a JavaScript exception. Evaluation returns it when JavaScript
// throws, and a Function that returns it throws an error with its Name, such
// as "TypeError", into JavaScript.
type Error struct {
	// Name is the error's constructor name, such as "TypeError", or empty
	// if a non-Error value was thrown.
	Name    string
	Message string
	Stack   string
}

func (e *Error) Error() string {
	msg := e.Message
	if e.Name != "" {
		msg = e.Name + ": " + msg
	}

	if e.Stack != "" {
		msg += "\n" + e.Stack
	}

	return msg
}

func newMaxLengthExceededErr(request uint, maxLen int64, index int) error {
	return fmt.Errorf("length %d exceeds max %d at index %d", request, maxLen, index)
}

func newOverflowErr(value any, targetType string) error {
	return fmt.Errorf("value %v overflows %s", value, targetType)
}

func newGoToJsErr(kind string, err error, details ...string) error {
	detail := ""
	if len(details) > 0 {
		detail = " " + details[0]
	}

	if err == nil {
		return fmt.Errorf("cannot convert Go%s '%s' to JS", detail, kind)
	}

	return fmt.Errorf("cannot convert Go%s '%s' to JS: %w", detail, kind, err)
}

func newJsToGoErr(kind *Value, err error, details ...string) error {
	detail := ""
	if len(details) > 0 {
		detail = " " + details[0]
	}

	kindStr := ""

	var kindErr error

	if kind != nil {
		kindStr, kindErr = kind.jsonStringify()
		if kindErr != nil {
			kindStr = fmt.Errorf("(%w), %s", kindErr, kind.String()).Error()
		}
	}

	if kindStr == "undefined" || kindStr == "null" {
		kindStr = kind.Type()
	}

	if kindStr != "" {
		kindStr = " " + kindStr
	}

	if err == nil {
		return fmt.Errorf("cannot convert JS%s%s to Go", detail, kindStr)
	}

	return fmt.Errorf("cannot convert JS%s%s to Go: %w", detail, kindStr, err)
}

func newArgConversionErr(index int, err error) error {
	return fmt.Errorf("cannot convert JS function argument at index %d: %w", index, err)
}

func newInvalidGoTypeErr(expect string, got any) error {
	return fmt.Errorf("expected GO type %s, got %T", expect, got)
}

func newInvalidJsInputErr(kind string, input *Value) (err error) {
	var detail string
	if detail, err = input.jsonStringify(); err != nil {
		detail = fmt.Sprintf("(JSONStringify failed: %v), (.String()) %s", err, input.String())
	}

	return fmt.Errorf("expected JS %s, got %s=%s", kind, input.Type(), detail)
}

func newJsStringifyErr(kind string, err error) error {
	return fmt.Errorf("js %s: %w", kind, err)
}

func newInvokeErr(input *Value, err error) error {
	return fmt.Errorf("cannot call getTime on JS value '%s', err=%w", input.String(), err)
}

func anyToError(err any) error {
	if err == nil {
		return nil
	}

	if e, ok := err.(error); ok {
		return e
	}

	if str, ok := err.(string); ok {
		return fmt.Errorf("recovered from panic: %s", str)
	}

	return fmt.Errorf("recovered from panic: %v", err)
}
