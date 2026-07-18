package sse

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReaderSkipsCommentsAndBlankLines(t *testing.T) {
	in := ": keepalive\n\n\nid: 7\nevent: update\ndata: {\"a\":1}\n\n"
	r := NewReader(strings.NewReader(in))

	f, err := r.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if f.ID != "7" || f.Event != "update" {
		t.Errorf("unexpected frame metadata: %+v", f)
	}
	if string(f.Data) != `{"a":1}` {
		t.Errorf("data = %q", f.Data)
	}
}

func TestReaderJoinsMultilineData(t *testing.T) {
	in := "data: line1\ndata: line2\n\n"
	f, err := NewReader(strings.NewReader(in)).Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if string(f.Data) != "line1\nline2" {
		t.Errorf("data = %q, want the two lines joined by a newline", f.Data)
	}
}

func TestReaderReturnsEOFAtStreamEnd(t *testing.T) {
	r := NewReader(strings.NewReader("data: one\n\n"))
	if _, err := r.Next(); err != nil {
		t.Fatalf("first Next: %v", err)
	}
	if _, err := r.Next(); !errors.Is(err, io.EOF) {
		t.Errorf("second Next err = %v, want EOF", err)
	}
}

func TestReaderHandlesFieldWithoutSpace(t *testing.T) {
	// The spec makes the space after the colon optional; the bridge is
	// consistent about including it, but nothing guarantees that forever.
	f, err := NewReader(strings.NewReader("data:{\"a\":1}\n\n")).Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if string(f.Data) != `{"a":1}` {
		t.Errorf("data = %q", f.Data)
	}
}
