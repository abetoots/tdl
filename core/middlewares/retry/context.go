package retry

import (
	"errors"
	"fmt"

	"github.com/gotd/td/tgerr"
)

type waitContextError struct {
	cause error
	text  string
}

func (e *waitContextError) Error() string { return e.text }

// match the context cause without exposing deadline exceeded as a net.Error.
func (e *waitContextError) Is(target error) bool { return target == e.cause }

// keep the last telegram error as text when a wait is interrupted.
func WaitContextError(ctxErr, lastErr error) error {
	var previous *waitContextError
	if errors.As(lastErr, &previous) && previous.Is(ctxErr) {
		return previous
	}
	if _, ok := tgerr.As(lastErr); !ok {
		return ctxErr
	}
	return &waitContextError{cause: ctxErr, text: fmt.Sprintf("%v: %v", ctxErr, lastErr)}
}
