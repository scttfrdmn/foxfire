package foxfire

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/time/rate"
)

const (
	basePath   = "/clip/v2"
	keyHeader  = "hue-application-key"
	maxBodyLen = 8 << 20 // the bridge never sends anything close to this
)

func (c *Client) url(path string) string {
	return "https://" + c.addr + basePath + path
}

func (c *Client) newRequest(ctx context.Context, method, url string, body []byte) (*http.Request, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set(keyHeader, c.appKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// getMany fetches a collection and returns the decoded data array. It is a
// package-level function rather than a method because Go does not permit type
// parameters on methods.
func getMany[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	req, err := c.newRequest(ctx, http.MethodGet, c.url(path), nil)
	if err != nil {
		return nil, err
	}
	var env resourceEnvelope[T]
	if err := c.do(req, &env); err != nil {
		return nil, err
	}
	return env.Data, nil
}

// getOne fetches a single resource by ID. The bridge returns a one-element
// array rather than a bare object, which we unwrap here so callers do not
// have to think about it.
func getOne[T any](ctx context.Context, c *Client, path string, id ID) (T, error) {
	var zero T
	items, err := getMany[T](ctx, c, fmt.Sprintf("%s/%s", path, id))
	if err != nil {
		return zero, err
	}
	if len(items) == 0 {
		return zero, fmt.Errorf("%w: %s/%s", ErrNotFound, path, id)
	}
	return items[0], nil
}

// put applies a partial update. limiter selects which token bucket to spend
// from; pass nil for endpoints that are not command paths (scene edits,
// metadata renames) so that they are not throttled alongside light traffic.
func put(ctx context.Context, c *Client, path string, id ID, body any, limiter *rate.Limiter) error {
	if limiter != nil {
		if err := limiter.Wait(ctx); err != nil {
			return fmt.Errorf("foxfire: rate limiter: %w", err)
		}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("foxfire: encoding update: %w", err)
	}
	req, err := c.newRequest(ctx, http.MethodPut,
		fmt.Sprintf("%s/%s", c.url(path), id), payload)
	if err != nil {
		return err
	}
	var env updateEnvelope
	return c.do(req, &env)
}

// post creates a resource and returns a reference to it. Unlike put, POST
// bodies carry no ID in the path -- the bridge assigns one and returns it in
// the update envelope's data array. limiter follows the same convention as
// put: pass nil for creation of configuration resources (rooms, zones, scenes)
// which are not command traffic and should not share the light buckets.
func post(ctx context.Context, c *Client, path string, body any, limiter *rate.Limiter) (Ref, error) {
	if limiter != nil {
		if err := limiter.Wait(ctx); err != nil {
			return Ref{}, fmt.Errorf("foxfire: rate limiter: %w", err)
		}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return Ref{}, fmt.Errorf("foxfire: encoding create: %w", err)
	}
	req, err := c.newRequest(ctx, http.MethodPost, c.url(path), payload)
	if err != nil {
		return Ref{}, err
	}
	var env updateEnvelope
	if err := c.do(req, &env); err != nil {
		return Ref{}, err
	}
	if len(env.Data) == 0 {
		// A create that reports no error but returns no reference is a bridge
		// contract violation; surface it rather than handing back a zero Ref
		// the caller would treat as a valid ID.
		return Ref{}, fmt.Errorf("foxfire: create at %s returned no resource reference", path)
	}
	return env.Data[0], nil
}

// del removes a resource by ID. Like scene edits, deletion is configuration
// rather than command traffic, so it does not spend from a light bucket.
func del(ctx context.Context, c *Client, path string, id ID) error {
	req, err := c.newRequest(ctx, http.MethodDelete,
		fmt.Sprintf("%s/%s", c.url(path), id), nil)
	if err != nil {
		return err
	}
	var env updateEnvelope
	return c.do(req, &env)
}

// do executes a request and decodes the envelope, mapping both HTTP status
// and the bridge's errors array onto Go errors.
func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("foxfire: %s %s: %w", req.Method, req.URL.Path, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyLen))
		_ = resp.Body.Close()
	}()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyLen))
	if err != nil {
		return fmt.Errorf("foxfire: reading response: %w", err)
	}

	// Decode first, even on non-2xx: the bridge puts its explanation in the
	// errors array and the status alone is rarely enough to act on.
	if len(raw) > 0 && out != nil {
		if err := json.Unmarshal(raw, out); err != nil && resp.StatusCode/100 == 2 {
			return fmt.Errorf("foxfire: decoding response: %w", err)
		}
	}

	var apiErrs []apiError
	switch v := out.(type) {
	case *updateEnvelope:
		apiErrs = v.Errors
	default:
		// resourceEnvelope[T] is generic, so we re-decode just the errors
		// field rather than attempting a type switch over every instantiation.
		var probe struct {
			Errors []apiError `json:"errors"`
		}
		_ = json.Unmarshal(raw, &probe)
		apiErrs = probe.Errors
	}

	return errorsFrom(resp.StatusCode, apiErrs)
}
