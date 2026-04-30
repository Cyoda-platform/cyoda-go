package openapivalidator

import (
	"bytes"
	"flag"
	"io"
	"net/http"
	"strings"
)

// captureSource lets the middleware extract the captured bytes and status
// from any teeWriter variant via a single interface check.
type captureSource interface {
	captureBytes() []byte
	captureStatus() int
}

// runFilterActive returns true if the suite was started with -run set to
// any non-empty value. Computed each call (cheap and avoids package-init
// ordering surprises during tests that toggle the flag).
func runFilterActive() bool {
	f := flag.Lookup("test.run")
	if f == nil {
		return false
	}
	return f.Value.String() != ""
}

// NewMiddleware returns an http.Handler middleware that:
//
//  1. Wraps the response writer with a teeWriter to capture status + body.
//  2. Calls the wrapped handler.
//  3. Synthesizes an *http.Response from the captured bytes and runs it
//     through the validator.
//  4. Appends mismatches to the package-level collector.
//  5. In ModeEnforce + -run-filtered runs: also calls t.Errorf on the
//     captured *testing.T (if any) so the requesting test fails immediately.
//     Wrapped in defer recover() in case the test has exited (fire-and-
//     forget pattern; captured *T may no longer be valid).
func NewMiddleware(v *Validator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tw := newTeeWriter(w)
			next.ServeHTTP(tw, r)

			cs, ok := tw.(captureSource)
			if !ok {
				// Should never happen — every teeWriter variant satisfies
				// captureSource via embedded *teeWriter.
				return
			}

			// Build a synthetic *http.Response for the validator.
			resp := &http.Response{
				StatusCode: cs.captureStatus(),
				Header:     w.Header(),
				Body:       io.NopCloser(bytes.NewReader(cs.captureBytes())),
				Request:    r,
			}
			mismatches := v.Validate(r.Context(), r, resp)
			for _, m := range mismatches {
				if t := TestTFromContext(r.Context()); t != nil {
					m.TestName = t.Name()
				} else {
					m.TestName = "unknown"
				}
				defaultCollector.append(m)

				if Mode == ModeEnforce && runFilterActive() {
					func() {
						defer func() { _ = recover() }()
						if t := TestTFromContext(r.Context()); t != nil {
							t.Errorf("openapi conformance: %s %s -> %d: %s",
								m.Method, m.Path, m.Status,
								strings.TrimSpace(m.Reason))
						}
					}()
				}
			}
		})
	}
}
