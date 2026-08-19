package webfunction

import (
	"encoding/json"
	"fmt"
)

// Path is an immutable JSONPath expression identifying a value within a
// not-yet-resolved pipeline response. Ported from the Ruby reference's
// Promise::Path.
type Path struct {
	raw string
}

// String returns the path's JSONPath string, e.g. "$[0].id".
func (p Path) String() string {
	return p.raw
}

// Field returns a new Path one level deeper, at the named field.
func (p Path) Field(name string) Path {
	return Path{raw: fmt.Sprintf("%s.%s", p.raw, name)}
}

// Index returns a new Path one level deeper, at the given array index.
func (p Path) Index(i int) Path {
	return Path{raw: fmt.Sprintf("%s[%d]", p.raw, i)}
}

// Promise stands in for a pipelined call's result before the pipeline has
// been executed. Ported from the Ruby reference's Promise.
//
// Simplification versus the Ruby original: Ruby's Promise#[] behaves
// differently once resolved (it indexes into the real, already-decoded
// value) versus unresolved (it extends the JSONPath). This Go port's
// Field/Index always just extend the path, regardless of resolution
// state - digging into an already-resolved value's real structure would
// need reflection-heavy navigation of a decoded any value, which isn't
// implemented. Call Value or Resolve to get the real value once you know
// the pipeline has run.
type Promise struct {
	pipeline *Pipeline
	path     Path
	value    any
	resolved bool
}

// Field returns a new Promise scoped one level deeper, at the named
// field.
func (pr *Promise) Field(name string) *Promise {
	return &Promise{pipeline: pr.pipeline, path: pr.path.Field(name)}
}

// Index returns a new Promise scoped one level deeper, at the given array
// index.
func (pr *Promise) Index(i int) *Promise {
	return &Promise{pipeline: pr.pipeline, path: pr.path.Index(i)}
}

// Path returns the promise's JSONPath string.
func (pr *Promise) Path() string {
	return pr.path.String()
}

// Value returns the promise's resolved value, or a *UnresolvedPromiseError
// if the pipeline hasn't been executed yet.
func (pr *Promise) Value() (any, error) {
	if !pr.resolved {
		return nil, newUnresolvedPromiseError(pr.path.String())
	}
	return pr.value, nil
}

// Resolve executes the owning pipeline if the promise isn't already
// resolved, then returns the value.
func (pr *Promise) Resolve() (any, error) {
	if pr.resolved {
		return pr.value, nil
	}
	if _, err := pr.pipeline.Execute(ReturnAll); err != nil {
		return nil, err
	}
	return pr.Value()
}

// MarshalJSON serializes the promise as its resolved value if resolved,
// or as its plain JSONPath string otherwise - matching the wire
// representation an unresolved pipeline reference takes when embedded as
// an argument to a later step. Per the pipelining spec, a literal
// argument string that happens to start with "$" must itself be escaped
// as "\$" so it isn't mistaken for a promise reference - that escaping is
// the caller's responsibility for plain string args, since a Promise
// value here is always an intentional reference, never a literal.
func (pr *Promise) MarshalJSON() ([]byte, error) {
	if pr.resolved {
		return json.Marshal(pr.value)
	}
	return json.Marshal(pr.path.String())
}

// PipelineReturns selects what a Pipeline.Execute call returns and which
// promises it fills in. Use ReturnAll, ReturnLast, or ReturnPath.
type PipelineReturns struct {
	raw string
}

// ReturnAll returns every step's result as an array and fills in every
// promise from the batch. This is the default per the reference spec.
var ReturnAll = PipelineReturns{raw: "all"}

// ReturnLast returns only the final step's result and fills in only the
// last promise.
var ReturnLast = PipelineReturns{raw: "last"}

// ReturnPath returns the value at the given JSONPath expression, passed
// through verbatim for the server to resolve. No promises are filled in
// automatically for this mode.
func ReturnPath(jsonpath string) PipelineReturns {
	return PipelineReturns{raw: jsonpath}
}

type pipelineStep struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    map[string]any    `json:"body"`
}

// Pipeline batches several endpoint calls into a single HTTP request. The
// server runs them in order; a step's arguments can reference an earlier
// step's not-yet-known result via a Promise. Ported from the Ruby
// reference's Pipeline.
type Pipeline struct {
	url        string
	steps      []pipelineStep
	promises   []*Promise
	httpClient HTTPDoer
}

// NewPipeline creates a Pipeline that executes against the given
// pipeline URL (a package's PipelineURL).
func NewPipeline(url string) *Pipeline {
	return &Pipeline{url: url}
}

// AddStep appends a step and returns a Promise standing in for its
// eventual result.
func (pl *Pipeline) AddStep(url string, headers map[string]string, body map[string]any) *Promise {
	n := len(pl.promises)
	promise := &Promise{pipeline: pl, path: Path{raw: fmt.Sprintf("$[%d]", n)}}

	pl.steps = append(pl.steps, pipelineStep{URL: url, Headers: headers, Body: body})
	pl.promises = append(pl.promises, promise)

	return promise
}

// Execute runs every step added since the last Execute (or since the
// pipeline was created), in one HTTP request, and resets the pipeline
// for the next batch.
func (pl *Pipeline) Execute(returns PipelineReturns) (any, error) {
	var returnsArg string
	switch returns.raw {
	case "all":
		returnsArg = "$"
	case "last":
		returnsArg = "$[-1:]"
	default:
		returnsArg = returns.raw
	}

	steps := pl.steps
	promises := pl.promises

	result, err := Execute(pl.url, ExecuteOptions{
		Args: map[string]any{
			"steps":   steps,
			"returns": returnsArg,
		},
		HTTPClient: pl.httpClient,
	})

	pl.reset()

	if err != nil {
		return nil, err
	}

	switch returns.raw {
	case "all":
		if arr, ok := result.([]any); ok {
			for i, v := range arr {
				if i < len(promises) {
					promises[i].resolved = true
					promises[i].value = v
				}
			}
		}
	case "last":
		if len(promises) > 0 {
			last := promises[len(promises)-1]
			last.resolved = true
			last.value = result
		}
	}

	return result, nil
}

func (pl *Pipeline) reset() {
	pl.steps = nil
	pl.promises = nil
}