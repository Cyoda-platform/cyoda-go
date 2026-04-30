package openapivalidator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

// Validator wraps the spec's router and validates HTTP responses against
// the matched operation's declared schema.
//
// IncludeResponseStatus=true is load-bearing: openapi3filter's default
// behavior is to silently pass undeclared status codes (verified against
// kin-openapi v0.137.0 openapi3filter/validate_response.go:48-58). Without
// this flag the validator misses an entire class of drift.
//
// MultiError=true accumulates all schema errors per response rather than
// failing on the first.
type Validator struct {
	doc    *openapi3.T
	router routers.Router
	opts   *openapi3filter.Options
}

// NewValidator builds a Validator from a parsed OpenAPI 3.1 document.
func NewValidator(doc *openapi3.T) (*Validator, error) {
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		return nil, fmt.Errorf("build router: %w", err)
	}
	return &Validator{
		doc:    doc,
		router: router,
		opts: &openapi3filter.Options{
			IncludeResponseStatus: true,
			MultiError:            true,
			AuthenticationFunc: func(ctx context.Context, ai *openapi3filter.AuthenticationInput) error {
				return nil // skip auth checks; we validate shape only
			},
		},
	}, nil
}

// Validate runs the response through openapi3filter.ValidateResponse and
// returns any mismatches it finds. Returns an empty slice on success.
//
// Records the matched operationId in the package's exercised set, regardless
// of whether validation passed.
//
// Wraps the underlying call in a panic recovery so that bugs in kin-openapi
// (or in our spec/wire data hitting an untested code path) surface as
// mismatch records rather than crashing the test server's request goroutine.
func (v *Validator) Validate(ctx context.Context, req *http.Request, resp *http.Response) (mismatches []Mismatch) {
	defer func() {
		if r := recover(); r != nil {
			mismatches = append(mismatches, Mismatch{
				Method: req.Method,
				Path:   req.URL.Path,
				Status: resp.StatusCode,
				Reason: fmt.Sprintf("validator panic: %v", r),
			})
		}
	}()
	route, _, err := v.router.FindRoute(req)
	if err != nil {
		// No matching route — the request hit a path the spec doesn't declare.
		// This is a real mismatch (handler exists for an undeclared route).
		return []Mismatch{{
			Method: req.Method,
			Path:   req.URL.Path,
			Status: resp.StatusCode,
			Reason: fmt.Sprintf("no spec route matches %s %s: %v", req.Method, req.URL.Path, err),
		}}
	}
	opId := route.Operation.OperationID
	defaultCollector.recordExercised(opId)

	// Streaming check: if the matched operation declares
	// application/x-ndjson for the actual status code, skip body validation.
	// kin-openapi's ValidateResponse panics if input.Body is nil (the
	// `defer body.Close()` line in validate_response.go), so we use a
	// dedicated streaming-only options copy with ExcludeResponseBody=true
	// AND pass a non-nil empty body for defense-in-depth.
	if v.isStreaming(route, resp.StatusCode) {
		streamingOpts := *v.opts // copy; do not mutate the shared opts
		streamingOpts.ExcludeResponseBody = true
		input := &openapi3filter.ResponseValidationInput{
			RequestValidationInput: &openapi3filter.RequestValidationInput{
				Request: req,
				Route:   route,
			},
			Status:  resp.StatusCode,
			Header:  resp.Header,
			Body:    io.NopCloser(strings.NewReader("")),
			Options: &streamingOpts,
		}
		if err := openapi3filter.ValidateResponse(ctx, input); err != nil {
			return v.toMismatches(err, opId, req, resp.StatusCode)
		}
		return nil
	}

	// Read response body for validation. The middleware passed the captured
	// bytes via resp.Body; we consume them here.
	// Options must be set on ResponseValidationInput — ValidateResponse reads
	// input.Options (not input.RequestValidationInput.Options).
	input := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{
			Request: req,
			Route:   route,
		},
		Status:  resp.StatusCode,
		Header:  resp.Header,
		Body:    resp.Body,
		Options: v.opts,
	}
	if err := openapi3filter.ValidateResponse(ctx, input); err != nil {
		return v.toMismatches(err, opId, req, resp.StatusCode)
	}
	return nil
}

// isStreaming reports whether the matched operation declares
// application/x-ndjson for the given status code.
func (v *Validator) isStreaming(route *routers.Route, status int) bool {
	if route.Operation == nil || route.Operation.Responses == nil {
		return false
	}
	resp := route.Operation.Responses.Status(status)
	if resp == nil || resp.Value == nil {
		return false
	}
	for ct := range resp.Value.Content {
		if ct == "application/x-ndjson" {
			return true
		}
	}
	return false
}

// toMismatches converts the kin-openapi error tree into one or more Mismatch
// records. MultiError is unwrapped so each schema problem becomes its own row.
func (v *Validator) toMismatches(err error, opId string, req *http.Request, status int) []Mismatch {
	var multi openapi3.MultiError
	if errors.As(err, &multi) {
		out := make([]Mismatch, 0, len(multi))
		for _, e := range multi {
			out = append(out, Mismatch{
				Operation: opId,
				Method:    req.Method,
				Path:      req.URL.Path,
				Status:    status,
				Reason:    e.Error(),
			})
		}
		return out
	}
	return []Mismatch{{
		Operation: opId,
		Method:    req.Method,
		Path:      req.URL.Path,
		Status:    status,
		Reason:    err.Error(),
	}}
}
