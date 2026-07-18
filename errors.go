package foxfire

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Sentinel errors callers are expected to match on.
var (
	// ErrLinkButtonNotPressed is returned by Pair when the physical button on
	// the bridge has not been pressed within the preceding 30 seconds.
	ErrLinkButtonNotPressed = errors.New("foxfire: link button not pressed")

	// ErrUnauthorized means the application key was rejected. Keys can be
	// revoked from the Hue app, so this is not necessarily a bug.
	ErrUnauthorized = errors.New("foxfire: unauthorized")

	// ErrNotFound means the resource ID does not exist on this bridge.
	ErrNotFound = errors.New("foxfire: resource not found")

	// ErrBridgeIdentity means the TLS peer did not present a certificate
	// matching the expected bridge ID. Treat this as hostile until proven
	// otherwise.
	ErrBridgeIdentity = errors.New("foxfire: bridge certificate identity mismatch")

	// ErrNoBridges is returned by Discover when neither mDNS nor the cloud
	// discovery endpoint yielded a bridge.
	ErrNoBridges = errors.New("foxfire: no bridges found")
)

// apiError is a single entry from the bridge's errors array.
type apiError struct {
	Description string `json:"description"`
}

// APIError aggregates the errors array returned by the bridge alongside the
// HTTP status, since the bridge will happily return 200 with a non-empty
// errors array for partially applied updates.
type APIError struct {
	StatusCode   int
	Descriptions []string
}

func (e *APIError) Error() string {
	if len(e.Descriptions) == 0 {
		return fmt.Sprintf("foxfire: bridge returned HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("foxfire: bridge returned HTTP %d: %s",
		e.StatusCode, strings.Join(e.Descriptions, "; "))
}

// Unwrap maps bridge statuses onto the sentinels so that errors.Is works for
// the cases callers actually branch on.
func (e *APIError) Unwrap() error {
	switch e.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrUnauthorized
	case http.StatusNotFound:
		return ErrNotFound
	}
	return nil
}

func errorsFrom(status int, in []apiError) error {
	if len(in) == 0 && status >= 200 && status < 300 {
		return nil
	}
	descs := make([]string, 0, len(in))
	for _, e := range in {
		descs = append(descs, e.Description)
	}
	if status >= 200 && status < 300 && len(descs) == 0 {
		return nil
	}
	return &APIError{StatusCode: status, Descriptions: descs}
}
