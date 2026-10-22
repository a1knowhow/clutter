package cuttle

import "errors"

var (
	ErrClosed      = errors.New("cuttle: store closed")
	ErrNotFound    = errors.New("cuttle: graph or assertion not found")
	ErrExists      = errors.New("cuttle: identity already exists")
	ErrConflict    = errors.New("cuttle: stale expected revision")
	ErrInvalid     = errors.New("cuttle: invalid input")
	ErrUnsupported = errors.New("cuttle: unsupported query feature")
	ErrCapacity    = errors.New("cuttle: capacity exceeded")
	ErrStranded    = errors.New("cuttle: relationships would be stranded")
	ErrCanceled    = errors.New("cuttle: operation canceled")
)
