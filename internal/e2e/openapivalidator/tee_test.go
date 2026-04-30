package openapivalidator

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTee_CapturesBytesAndForwards(t *testing.T) {
	rec := httptest.NewRecorder()
	tee := newTeeWriter(rec).(*teeWriter)

	tee.WriteHeader(201)
	if _, err := tee.Write([]byte(`{"ok":true}`)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if got := rec.Code; got != 201 {
		t.Errorf("forwarded status = %d, want 201", got)
	}
	if got := rec.Body.String(); got != `{"ok":true}` {
		t.Errorf("forwarded body = %q, want %q", got, `{"ok":true}`)
	}
	if got := tee.captured.String(); got != `{"ok":true}` {
		t.Errorf("captured body = %q, want %q", got, `{"ok":true}`)
	}
	if got := tee.status; got != 201 {
		t.Errorf("captured status = %d, want 201", got)
	}
}

type flusherRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (f *flusherRecorder) Flush() { f.flushed = true; f.ResponseRecorder.Flush() }

func TestTee_DelegatesFlusher(t *testing.T) {
	rec := &flusherRecorder{ResponseRecorder: httptest.NewRecorder()}
	tee := newTeeWriter(rec)

	flusher, ok := tee.(http.Flusher)
	if !ok {
		t.Fatal("tee does not implement http.Flusher when underlying does")
	}
	flusher.Flush()
	if !rec.flushed {
		t.Error("Flush did not delegate to underlying writer")
	}
}

type readerFromRecorder struct {
	*httptest.ResponseRecorder
	readFromCalled bool
}

func (r *readerFromRecorder) ReadFrom(src io.Reader) (int64, error) {
	r.readFromCalled = true
	return io.Copy(r.ResponseRecorder, src)
}

func TestTee_DelegatesReaderFrom(t *testing.T) {
	rec := &readerFromRecorder{ResponseRecorder: httptest.NewRecorder()}
	tee := newTeeWriter(rec)

	rf, ok := tee.(io.ReaderFrom)
	if !ok {
		t.Fatal("tee does not implement io.ReaderFrom when underlying does")
	}
	src := strings.NewReader("hello")
	if _, err := rf.ReadFrom(src); err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if !rec.readFromCalled {
		t.Error("ReadFrom did not delegate to underlying writer")
	}
	// Find the underlying *teeWriter to inspect captured bytes.
	type captureGetter interface{ captureBytes() []byte }
	cg, ok := tee.(captureGetter)
	if !ok {
		// Variant struct without accessor method yet; test direct field access via embedded pointer.
		// The variant types embed *teeWriter; reach in.
		switch v := tee.(type) {
		case *teeR:
			if got := v.captured.String(); got != "hello" {
				t.Errorf("captured = %q, want %q", got, "hello")
			}
			return
		case *teeFR:
			if got := v.captured.String(); got != "hello" {
				t.Errorf("captured = %q, want %q", got, "hello")
			}
			return
		case *teeHR:
			if got := v.captured.String(); got != "hello" {
				t.Errorf("captured = %q, want %q", got, "hello")
			}
			return
		case *teeFHR:
			if got := v.captured.String(); got != "hello" {
				t.Errorf("captured = %q, want %q", got, "hello")
			}
			return
		default:
			t.Fatalf("unexpected tee type %T", tee)
		}
	}
	if got := string(cg.captureBytes()); got != "hello" {
		t.Errorf("captured = %q, want %q", got, "hello")
	}
}

type hijackerRecorder struct {
	*httptest.ResponseRecorder
}

func (h *hijackerRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, http.ErrNotSupported
}

func TestTee_DelegatesHijacker(t *testing.T) {
	rec := &hijackerRecorder{ResponseRecorder: httptest.NewRecorder()}
	tee := newTeeWriter(rec)

	if _, ok := tee.(http.Hijacker); !ok {
		t.Fatal("tee does not implement http.Hijacker when underlying does")
	}
}

func TestTee_DefaultStatusIs200(t *testing.T) {
	rec := httptest.NewRecorder()
	tee := newTeeWriter(rec).(*teeWriter)
	if _, err := tee.Write([]byte("body")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if tee.status != 200 {
		t.Errorf("default status after implicit WriteHeader = %d, want 200", tee.status)
	}
}

var _ = bytes.NewReader // keep import in case future tests need it
