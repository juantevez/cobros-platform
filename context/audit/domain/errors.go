package domain

import "errors"

var (
	ErrInvalidAction       = errors.New("invalid audit action (want <context>.<fact>, lowercase)")
	ErrInvalidResourceType = errors.New("invalid audit resource type")
)
