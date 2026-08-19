package webfunction

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// moduleVersion is sent in the User-Agent header, mirroring the Ruby
// reference client's "webfunction/<gem version>". Bumped by hand for now;
// worth wiring up to a real release process later.
const moduleVersion = "0.1.0"

// HTTPDoer is the interface required of a pluggable HTTP client. *http.
// Client satisfies it. Set via WithHTTPClient on a Client, or pass one
// directly to Execute via ExecuteOptions.
//
// This is the Go equivalent of the Ruby reference's swappable
// Request.http_client proc.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ExecuteOptions configures a single low-level request.
type ExecuteOptions struct {
	// BearerAuth, if set, is sent as "Authorization: Bearer <token>".
	BearerAuth string

	// Version, if set, is sent as the "Api-Version" header.
	Version string

	// Args is the JSON object body to POST. A nil map is sent as `{}`.
	Args map[string]any

	// HTTPClient, if set, is used instead of http.DefaultClient.
	HTTPClient HTTPDoer
}

// headerMap builds the standard request headers as a plain string map -
// used both for the real HTTP request (via headers()) and for a
// pipeline step's "headers" field, which the pipeline endpoint expects as
// plain JSON.
func (o ExecuteOptions) headerMap() map[string]string {
	h := map[string]string{
		"Content-Type":    "application/json",
		"Accept":          "application/json",
		"User-Agent":      "webfunction-go/" + moduleVersion,
		"Accept-Encoding": "gzip",
	}
	if o.BearerAuth != "" {
		h["Authorization"] = "Bearer " + o.BearerAuth
	}
	if o.Version != "" {
		h["Api-Version"] = o.Version
	}
	return h
}

func (o ExecuteOptions) headers() http.Header {
	h := http.Header{}
	for k, v := range o.headerMap() {
		h.Set(k, v)
	}
	return h
}

// Execute invokes a single Web Function endpoint URL directly via HTTP,
// without needing a Package. Mirrors the Ruby reference's low-level
// WebFunction::Request.execute / WebFunction::Request.new(...).execute.
//
// It does not wrap paginated results in a Page - callers that know an
// endpoint is paginated (via Endpoint.Paginated()) should wrap the result
// themselves; see Client.Call for the normal path.
//
// Returns the decoded JSON response value (map[string]any, []any, string,
// float64, bool, or nil) on success (status 200). Returns a
// *BadRequestError on a 400, a *JsonParseError if the body isn't valid
// JSON, or a *UnexpectedStatusCodeError for any other status.
func Execute(url string, opts ExecuteOptions) (any, error) {
	args := opts.Args
	if args == nil {
		args = map[string]any{}
	}
	body, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("encoding request body: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building request for %s: %w", url, err)
	}
	req.Header = opts.headers()

	doer := opts.HTTPClient
	if doer == nil {
		doer = http.DefaultClient
	}

	resp, err := doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request to %s: %w", url, err)
	}
	defer resp.Body.Close()

	rawBody, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("reading response from %s: %w", url, err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		return nil, newUnexpectedStatusCodeError(resp.StatusCode, string(rawBody))
	}

	var result any
	if len(rawBody) > 0 {
		if err := json.Unmarshal(rawBody, &result); err != nil {
			return nil, newJsonParseError(err, resp.StatusCode, string(rawBody))
		}
	}

	if resp.StatusCode == http.StatusBadRequest {
		code := "WFN_BAD_REQUEST_ERROR"
		message := "Bad request"
		var details any = map[string]any{"body": result}

		if triple, ok := result.([]any); ok && len(triple) == 3 {
			c, cOK := triple[0].(string)
			m, mOK := triple[1].(string)
			if cOK && mOK {
				code = c
				message = m
				details = triple[2]
			}
		}

		return nil, newBadRequestError(code, message, details)
	}

	return result, nil
}

// readResponseBody reads resp.Body, transparently gunzipping it if the
// server compressed it (Content-Encoding: gzip).
//
// This is needed because we set our own "Accept-Encoding: gzip" header
// (to match the reference clients' request headers) - Go's net/http
// Transport only auto-decompresses gzip responses when *it* added that
// header itself; if the caller sets Accept-Encoding explicitly, as we do,
// Transport leaves the response body exactly as the server sent it and
// does no decompression. Without this, a gzip-compressing server (like
// api.reservepay.com) would hand back raw gzip bytes that fail JSON
// parsing with something like "invalid character '\x1f' looking for
// beginning of value" - 0x1f being the gzip magic byte.
func readResponseBody(resp *http.Response) ([]byte, error) {
	if resp.Header.Get("Content-Encoding") != "gzip" {
		return io.ReadAll(resp.Body)
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("decompressing gzip response: %w", err)
	}
	defer gz.Close()
	return io.ReadAll(gz)
}