package internal

import (
	"errors"
	"fmt"
)

// The manager reports failures as errors that say what kind of failure they
// are, so the HTTP layer can pick a status code without re-deriving it from
// the wording. A bare boolean could not: "no such server" and "the interface
// refused to come up" both came back as false and were answered with 404.
var (
	// ErrNotFound means the server or client in the request path does not
	// exist. Answered with 404.
	ErrNotFound = errors.New("not found")

	// ErrInvalid means the request itself is wrong - a malformed body, a
	// value outside its allowed range. Answered with 400.
	ErrInvalid = errors.New("invalid request")

	// ErrConflict means the request is well formed but cannot apply to the
	// current state, such as activating a client that is not suspended.
	// Answered with 409.
	ErrConflict = errors.New("conflicting state")
)

// serverNotFound and clientNotFound name what was missing while staying
// recognisable as ErrNotFound.
func serverNotFound(serverID string) error {
	return fmt.Errorf("server %s: %w", serverID, ErrNotFound)
}

func clientNotFound(clientID string) error {
	return fmt.Errorf("client %s: %w", clientID, ErrNotFound)
}
