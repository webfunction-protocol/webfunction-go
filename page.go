package webfunction

import "fmt"

// Page holds one page of results from a paginated endpoint.
//
// Paginated responses follow the Web Function pagination contract: a JSON
// object with "page", "next", and "previous" keys. The "next" and
// "previous" values are opaque request bodies - call NextPage or
// PreviousPage to fetch the adjacent page; never construct or inspect them
// yourself.
//
// Unlike the Ruby/JS/PHP reference clients, wrapping here is triggered by
// the endpoint's declared "paginated" flag (see Endpoint.Paginated), not
// by sniffing the response shape - a deliberate deviation, see
// package.go's Endpoint.Paginated doc comment.
//
// See https://webfunction.org/pagination for the full contract.
type Page struct {
	items    []any
	next     map[string]any
	previous map[string]any

	url        string
	bearerAuth string
	version    string
	httpClient HTTPDoer
}

// Items returns the items on the current page.
func (p *Page) Items() []any {
	return p.items
}

// HasNext reports whether a next page is available.
func (p *Page) HasNext() bool {
	return p.next != nil
}

// HasPrevious reports whether a previous page is available.
func (p *Page) HasPrevious() bool {
	return p.previous != nil
}

// NextPage fetches the next page by posting the opaque "next" body back to
// the same endpoint URL. Returns (nil, nil) if there is no next page.
func (p *Page) NextPage() (*Page, error) {
	return p.fetch(p.next)
}

// PreviousPage fetches the previous page by posting the opaque "previous"
// body back to the same endpoint URL. Returns (nil, nil) if there is no
// previous page.
func (p *Page) PreviousPage() (*Page, error) {
	return p.fetch(p.previous)
}

func (p *Page) fetch(body map[string]any) (*Page, error) {
	if body == nil {
		return nil, nil
	}

	result, err := Execute(p.url, ExecuteOptions{
		BearerAuth: p.bearerAuth,
		Version:    p.version,
		Args:       body,
		HTTPClient: p.httpClient,
	})
	if err != nil {
		return nil, err
	}

	return wrapPage(result, p.url, p.bearerAuth, p.version, p.httpClient)
}

// wrapPage wraps a decoded response in a Page. Used by Client.Call for
// endpoints flagged as paginated. Returns an error if the response
// doesn't actually match the {page, next, previous} contract shape -
// which, since wrapping is now flag-driven rather than shape-sniffed,
// signals a real mismatch between the endpoint's declared flag and its
// actual response worth surfacing rather than silently ignoring.
func wrapPage(result any, url, bearerAuth, version string, httpClient HTTPDoer) (*Page, error) {
	obj, ok := result.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("endpoint is flagged paginated but response was not an object (got %T)", result)
	}

	rawItems, ok := obj["page"]
	if !ok {
		return nil, fmt.Errorf(`endpoint is flagged paginated but response has no "page" key`)
	}
	items, ok := rawItems.([]any)
	if !ok {
		return nil, fmt.Errorf(`endpoint is flagged paginated but "page" is not an array (got %T)`, rawItems)
	}

	next, err := asOpaqueBody(obj, "next")
	if err != nil {
		return nil, err
	}
	previous, err := asOpaqueBody(obj, "previous")
	if err != nil {
		return nil, err
	}

	return &Page{
		items:      items,
		next:       next,
		previous:   previous,
		url:        url,
		bearerAuth: bearerAuth,
		version:    version,
		httpClient: httpClient,
	}, nil
}

func asOpaqueBody(obj map[string]any, key string) (map[string]any, error) {
	raw, ok := obj[key]
	if !ok || raw == nil {
		return nil, nil
	}
	body, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("paginated response's %q key is not an object or null (got %T)", key, raw)
	}
	return body, nil
}