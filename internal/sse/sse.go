// Package sse implements just enough of the server-sent events wire format
// for the Hue bridge's event stream. The full spec has features the bridge
// does not use (retry hints, comment keepalives beyond ": hi", multi-line
// data with embedded blank lines); implementing them would be dead code.
package sse

import (
	"bufio"
	"bytes"
	"io"
	"strings"
)

// Frame is one dispatched event.
type Frame struct {
	ID    string
	Event string
	Data  []byte
}

// Reader parses frames from a stream.
type Reader struct {
	sc *bufio.Scanner
}

func NewReader(r io.Reader) *Reader {
	sc := bufio.NewScanner(r)
	// Event batches during a scene recall across a large install can be
	// substantial; the default 64KiB token limit is not enough.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return &Reader{sc: sc}
}

// Next blocks until a frame with a non-empty data payload is available, or
// the underlying stream ends. Keepalive comments are consumed silently.
func (r *Reader) Next() (Frame, error) {
	var f Frame
	var data bytes.Buffer

	for r.sc.Scan() {
		line := r.sc.Text()

		// A blank line dispatches the accumulated frame.
		if line == "" {
			if data.Len() == 0 {
				continue // keepalive or stray separator
			}
			f.Data = bytes.TrimSuffix(data.Bytes(), []byte("\n"))
			return f, nil
		}

		if strings.HasPrefix(line, ":") {
			continue // comment; the bridge sends these as heartbeats
		}

		field, value, found := strings.Cut(line, ":")
		if !found {
			field, value = line, ""
		}
		value = strings.TrimPrefix(value, " ")

		switch field {
		case "id":
			f.ID = value
		case "event":
			f.Event = value
		case "data":
			data.WriteString(value)
			data.WriteByte('\n')
		}
	}

	if err := r.sc.Err(); err != nil {
		return Frame{}, err
	}
	return Frame{}, io.EOF
}
