package pisession

import "errors"

var (
	ErrCommandTimeout = errors.New("pi RPC command timed out")
	ErrInvalidCursor  = errors.New("invalid pi session cursor")
	ErrProcessExited  = errors.New("pi process exited")
)
