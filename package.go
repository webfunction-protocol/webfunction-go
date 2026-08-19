package webfunction

// Package is the top-level Web Function package definition.
// See https://webfunction.org/package#package-definition.
//
// NOTE: this intentionally has no EventSourceURL/Events fields. An earlier
// Go model (in the wfn CLI's own webfunction/ package) included them, but
// they don't appear anywhere in the Ruby reference client (github.com/
// robinclart/web_function) - no event_source_url, no Event type, no
// "event_source" flag usage. Whether "events" are a real, still-unimplemented
// part of the webfunction.org spec, or an earlier over-read of the spec
// site, is unconfirmed. Left out here until that's resolved.
type Package struct {
	BaseURL     string     `json:"base_url"`
	PipelineURL string     `json:"pipeline_url,omitempty"`
	Name        string     `json:"name,omitempty"`
	Flags       []string   `json:"flags,omitempty"`
	Version     string     `json:"version,omitempty"`
	Versions    []string   `json:"versions,omitempty"`
	Docs        string     `json:"docs,omitempty"`
	Endpoints   []Endpoint `json:"endpoints"`
	Errors      []ErrorDef `json:"errors,omitempty"`
	Objects     []Object   `json:"objects,omitempty"`
}

// Versioned reports whether the package declares the "versioned" flag.
func (p *Package) Versioned() bool {
	return hasFlag(p.Flags, "versioned")
}

// Endpoint returns the named endpoint, or nil if the package has none by
// that name. Matches both hyphenated ("find-user") and underscored
// ("find_user") forms, mirroring the Ruby reference's Package#endpoint.
func (p *Package) Endpoint(name string) *Endpoint {
	name = dashify(name)
	for i := range p.Endpoints {
		if p.Endpoints[i].Name == name {
			return &p.Endpoints[i]
		}
	}
	return nil
}

// Object returns the named object definition, or nil if the package has
// none by that name. This is the raw lookup with no context filtering -
// see ObjectInContext for the context-aware version.
func (p *Package) Object(name string) *Object {
	for i := range p.Objects {
		if p.Objects[i].Name == name {
			return &p.Objects[i]
		}
	}
	return nil
}

// ObjectContext selects which member set an ObjectContext lookup resolves
// to - see ObjectInContext.
type ObjectContext int

const (
	// ArgumentContext selects an object's Arguments - used when the
	// object is referenced as an argument's type.
	ArgumentContext ObjectContext = iota
	// AttributeContext selects an object's Attributes - used when the
	// object is referenced as an endpoint's return type or an
	// attribute's type.
	AttributeContext
)

// ObjectInContext looks up a named object definition, resolved for the
// given context. Mirrors the Ruby reference's Package#object(name,
// context:): an object MAY define both Arguments and Attributes, since it
// can be referenced in both contexts across a package, so the caller must
// say which set applies. Returns nil if the object doesn't exist, or
// defines no members for the requested context.
func (p *Package) ObjectInContext(name string, ctx ObjectContext) *Object {
	obj := p.Object(name)
	if obj == nil {
		return nil
	}
	switch ctx {
	case ArgumentContext:
		if len(obj.Arguments) == 0 {
			return nil
		}
	case AttributeContext:
		if len(obj.Attributes) == 0 {
			return nil
		}
	}
	return obj
}

// Error returns the named package-level error definition, or nil.
func (p *Package) Error(code string) *ErrorDef {
	for i := range p.Errors {
		if p.Errors[i].Code == code {
			return &p.Errors[i]
		}
	}
	return nil
}

// Endpoint describes a single callable endpoint within a package.
// See https://webfunction.org/package#endpoint-definition.
type Endpoint struct {
	Name       string      `json:"name"`
	Returns    Type        `json:"returns"`
	Flags      []string    `json:"flags,omitempty"`
	Group      string      `json:"group,omitempty"`
	Docs       string      `json:"docs,omitempty"`
	Errors     []ErrorDef  `json:"errors,omitempty"`
	Arguments  []Argument  `json:"arguments"`
	Attributes []Attribute `json:"attributes,omitempty"`
}

// HasFlag reports whether the endpoint declares the given flag
// (e.g. "paginated", "bearer_auth", "private", "error_triple",
// "capture_bearer").
func (e *Endpoint) HasFlag(flag string) bool {
	return hasFlag(e.Flags, flag)
}

// Paginated reports whether the endpoint declares the "paginated" flag.
//
// Per Jon's explicit decision, pagination in this client is detected via
// this flag, not by sniffing the response shape the way the Ruby
// reference client does (Page.wrap running shape-detection on every
// response) - a deliberate deviation from the reference implementation.
func (e *Endpoint) Paginated() bool {
	return e.HasFlag("paginated")
}

// BearerAuth reports whether the endpoint requires a bearer token.
func (e *Endpoint) BearerAuth() bool {
	return e.HasFlag("bearer_auth")
}

// Private reports whether the endpoint is internal-only and should be
// omitted from generated docs or codegen output.
func (e *Endpoint) Private() bool {
	return e.HasFlag("private")
}

// Argument returns the named argument, or nil if the endpoint has none by
// that name.
func (e *Endpoint) Argument(name string) *Argument {
	for i := range e.Arguments {
		if e.Arguments[i].Name == name {
			return &e.Arguments[i]
		}
	}
	return nil
}

// Attribute returns the named returned attribute, or nil if the endpoint
// has none by that name.
func (e *Endpoint) Attribute(name string) *Attribute {
	for i := range e.Attributes {
		if e.Attributes[i].Name == name {
			return &e.Attributes[i]
		}
	}
	return nil
}

// Error returns the named endpoint-level error definition, or nil.
func (e *Endpoint) Error(code string) *ErrorDef {
	for i := range e.Errors {
		if e.Errors[i].Code == code {
			return &e.Errors[i]
		}
	}
	return nil
}

// Argument describes a single argument accepted by an endpoint.
// See https://webfunction.org/package#argument-definition.
type Argument struct {
	Name    string   `json:"name"`
	Type    Type     `json:"type"`
	Group   string   `json:"group,omitempty"`
	Choices []any    `json:"choices,omitempty"`
	Flags   []string `json:"flags,omitempty"`
	Docs    string   `json:"docs,omitempty"`
}

// Required reports whether the argument declares the "required" flag.
func (a *Argument) Required() bool {
	return hasFlag(a.Flags, "required")
}

// Optional reports whether the argument is not required.
func (a *Argument) Optional() bool {
	return !a.Required()
}

// Attribute describes a single attribute of an object returned by an
// endpoint.
// See https://webfunction.org/package#attribute-definition.
type Attribute struct {
	Name   string   `json:"name"`
	Type   Type     `json:"type"`
	Values []any    `json:"values,omitempty"`
	Flags  []string `json:"flags,omitempty"`
	Docs   string   `json:"docs,omitempty"`
}

// Nullable reports whether the attribute declares the "nullable" flag.
//
// Per spec this means more than just "the value may be null": the key MAY
// be absent from the object entirely, and when present its value MAY be
// null. Consumers SHOULD treat a missing key and a null value equivalently.
func (a *Attribute) Nullable() bool {
	return hasFlag(a.Flags, "nullable")
}

// Object is a named, reusable object type definition, referenced elsewhere
// in the package as a refined "object.<n>" type.
// See https://webfunction.org/package#object-definition.
//
// Per the spec, the same object name can carry two different member lists
// depending on the context it's referenced from: Arguments when the object
// is referenced as an argument's type, Attributes when referenced as an
// endpoint's return type or an attribute's type. An object MAY define
// both, if it's referenced in both contexts somewhere in the package. See
// ObjectInContext for context-aware lookup.
type Object struct {
	Name       string      `json:"name"`
	Arguments  []Argument  `json:"arguments,omitempty"`
	Attributes []Attribute `json:"attributes,omitempty"`
}

// ErrorDef describes a single named ERROR_CODE a package or endpoint can
// return. Named ErrorDef (not Error) to avoid colliding with Go's built-in
// error interface.
// See https://webfunction.org/package#error-definition.
type ErrorDef struct {
	Code string `json:"code"`
	Docs string `json:"docs,omitempty"`
}

func hasFlag(flags []string, want string) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}

// dashify converts underscores to hyphens, so idiomatic Go-ish or Ruby/
// Python-ish caller-supplied names ("find_user") match the hyphenated
// endpoint names used on the wire ("find-user").
func dashify(name string) string {
	out := []byte(name)
	for i, c := range out {
		if c == '_' {
			out[i] = '-'
		}
	}
	return string(out)
}