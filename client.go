package webfunction

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// Options configures a Client at construction time. All fields are
// optional.
type Options struct {
	// BearerAuth, if set, is sent as "Authorization: Bearer <token>" on
	// every call.
	BearerAuth string

	// Version, if set, is sent as the "Api-Version" header (or, for
	// FromURL, as an "api_version" query parameter) on every call.
	Version string

	// Pipelined, if true, batches calls into a single HTTP request
	// instead of executing them immediately - see Client.SetPipeline
	// and the Pipeline type. Only takes effect if the package declares a
	// PipelineURL; otherwise the client behaves as if false.
	Pipelined bool

	// HTTPClient, if set, is used instead of http.DefaultClient for
	// every request this client makes (including fetching the package
	// itself, and pipeline execution).
	HTTPClient HTTPDoer
}

// Client wraps a Package and provides a way to invoke its endpoints.
//
// Unlike the Ruby/JS/PHP reference clients, there is no dynamic dispatch
// here (Go has no Proxy/method_missing/__call equivalent) - every call
// goes through the explicit Call method. Generated named methods (e.g. a
// future ListContacts(args) wrapping Call("list-contacts", args)) are
// entirely the wfn CLI's future `go` codegen target's responsibility, not
// this library's.
type Client struct {
	pkg        *Package
	baseURL    string
	bearerAuth string
	version    string
	pipeline   *Pipeline
	httpClient HTTPDoer
}

// FromPackageEndpoint fetches the package by invoking url as a Web
// Function endpoint (an HTTP POST, per the package retrieval spec) and
// builds a Client from it.
func FromPackageEndpoint(endpointURL string, opts Options) (*Client, error) {
	result, err := Execute(endpointURL, ExecuteOptions{
		BearerAuth: opts.BearerAuth,
		Version:    opts.Version,
		HTTPClient: opts.HTTPClient,
	})
	if err != nil {
		return nil, err
	}

	pkg, err := packageFromValue(result)
	if err != nil {
		return nil, fmt.Errorf("parsing package from %s: %w", endpointURL, err)
	}

	return FromPackage(pkg, opts), nil
}

// FromURL fetches the package as plain JSON via an HTTP GET, rather than
// invoking it as a Web Function endpoint. Use this when the package
// document is served as a static JSON file instead of a POST-able
// endpoint. When opts.Version is set, it's sent as an "api_version" query
// parameter rather than an Api-Version header, matching how a plain GET
// has no room for the usual endpoint-invocation headers.
func FromURL(rawURL string, opts Options) (*Client, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parsing url %s: %w", rawURL, err)
	}
	if opts.Version != "" {
		q := u.Query()
		q.Set("api_version", opts.Version)
		u.RawQuery = q.Encode()
	}

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("building request for %s: %w", rawURL, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "webfunction-go/"+moduleVersion)
	req.Header.Set("Accept-Encoding", "gzip")

	doer := opts.HTTPClient
	if doer == nil {
		doer = http.DefaultClient
	}

	resp, err := doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching package from %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response from %s: %w", rawURL, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching package from %s: unexpected status %d", rawURL, resp.StatusCode)
	}

	var pkg Package
	if err := json.Unmarshal(body, &pkg); err != nil {
		return nil, fmt.Errorf("parsing package from %s: %w", rawURL, err)
	}

	return FromPackage(&pkg, opts), nil
}

// FromPackage builds a Client directly from an already-loaded Package,
// making no request of its own.
func FromPackage(pkg *Package, opts Options) *Client {
	var pipeline *Pipeline
	if opts.Pipelined && pkg.PipelineURL != "" {
		pipeline = NewPipeline(pkg.PipelineURL)
		pipeline.httpClient = opts.HTTPClient
	}

	return &Client{
		pkg:        pkg,
		baseURL:    pkg.BaseURL,
		bearerAuth: opts.BearerAuth,
		version:    opts.Version,
		pipeline:   pipeline,
		httpClient: opts.HTTPClient,
	}
}

// Package returns the Package this client wraps.
func (c *Client) Package() *Package {
	return c.pkg
}

// SetBearerAuth updates the bearer token used on subsequent calls.
func (c *Client) SetBearerAuth(token string) {
	c.bearerAuth = token
}

// SetVersion updates the API version used on subsequent calls.
func (c *Client) SetVersion(version string) {
	c.version = version
}

// SetPipeline replaces the client's pipeline. Pass nil to make calls
// execute immediately again instead of batching.
func (c *Client) SetPipeline(p *Pipeline) {
	c.pipeline = p
}

// Call invokes the named endpoint with the given arguments.
//
// If the client is unpipelined (the common case), the return value is the
// decoded response - a map[string]any, []any, string, float64, bool, nil,
// or *Page for an endpoint flagged "paginated" (see Endpoint.Paginated).
//
// If the client is pipelined (see Options.Pipelined / SetPipeline), the
// call is queued as a pipeline step instead of executed immediately, and
// the return value is a *Promise standing in for the eventual result -
// callers that use pipelining need to type-assert the returned any to
// *Promise. This mirrors the Ruby reference's polymorphic return (a value
// normally, a Promise when pipelined) as closely as Go's static typing
// allows.
func (c *Client) Call(endpointName string, args map[string]any) (any, error) {
	name := dashify(endpointName)

	endpointURL, err := joinEndpointURL(c.baseURL, name)
	if err != nil {
		return nil, err
	}

	endpoint := c.pkg.Endpoint(name)

	if c.pipeline != nil {
		opts := ExecuteOptions{BearerAuth: c.bearerAuth, Version: c.version}
		return c.pipeline.AddStep(endpointURL, opts.headerMap(), args), nil
	}

	result, err := Execute(endpointURL, ExecuteOptions{
		BearerAuth: c.bearerAuth,
		Version:    c.version,
		Args:       args,
		HTTPClient: c.httpClient,
	})
	if err != nil {
		return nil, err
	}

	if endpoint != nil && endpoint.Paginated() {
		return wrapPage(result, endpointURL, c.bearerAuth, c.version, c.httpClient)
	}

	return result, nil
}

func packageFromValue(v any) (*Package, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var pkg Package
	if err := json.Unmarshal(b, &pkg); err != nil {
		return nil, err
	}
	return &pkg, nil
}

// joinEndpointURL joins a package's base URL with an endpoint name,
// mirroring the Ruby reference's URI.join(base_url, endpoint_name) via
// RFC 3986 relative reference resolution - e.g. a base_url ending in "/"
// appends name as a new path segment, while one without treats name as
// replacing the last segment, same as Ruby's URI.join.
//
// Deliberately uses ResolveReference rather than url.URL.JoinPath: JoinPath
// was only added in Go 1.19, and this library targets any Go version (see
// the pagination-design decision to avoid Go 1.23+ iterators for the same
// reason) - ResolveReference has been in net/url since Go 1.0.
func joinEndpointURL(baseURL, name string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parsing base url %s: %w", baseURL, err)
	}
	ref, err := url.Parse(name)
	if err != nil {
		return "", fmt.Errorf("parsing endpoint name %s: %w", name, err)
	}
	return base.ResolveReference(ref).String(), nil
}