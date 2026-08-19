# webfunction-go

A Go client library for the [Web Function](https://webfunction.org) protocol.

Ported from and cross-checked against the reference Ruby gem
([robinclart/web_function](https://github.com/robinclart/web_function)), with
adjustments where Go's language model genuinely differs (no dynamic
dispatch, no exceptions, no operator overloading) or where a deliberate
design choice was made instead of following the reference exactly.

## Status

Functionally complete for a first pass: package fetching (as an endpoint or
plain JSON), calling endpoints, pagination, typed errors, pipelining, and
recursive type validation. Not yet published anywhere - `go.mod`'s module
path (`webfunction-go`) is a placeholder until a real hosting decision is
made.

## Usage

```go
client, err := webfunction.FromPackageEndpoint("https://api.example.com/package", webfunction.Options{
	BearerAuth: "token",
})
if err != nil {
	// ...
}

result, err := client.Call("find-user", map[string]any{"id": "123"})
```

There are no generated, named methods (e.g. `client.FindUser(...)`) - Go has
no equivalent of the reference clients' dynamic dispatch (Ruby's
`method_missing`, JS's `Proxy`, PHP's `__call`). Named per-endpoint methods
are the responsibility of the wfn CLI's future `go` codegen target, built on
top of this library, not something this library does itself.

### Pagination

An endpoint flagged `paginated` in the package definition returns a `*Page`
instead of a raw value:

```go
result, _ := client.Call("list-people", nil)
page := result.(*webfunction.Page)

for _, item := range page.Items() {
	// ...
}
if page.HasNext() {
	next, _ := page.NextPage()
}
```

**Deviation from the reference clients**: the Ruby/JS/PHP clients detect
pagination by inspecting each response's *shape* (`{page, next, previous}`),
regardless of any flag. This client instead trusts the endpoint's declared
`paginated` flag and treats a flag/shape mismatch as an error. This was a
deliberate choice, not an oversight.

### Errors

Every failure is a Go `error`. Four concrete types - `*BadRequestError`,
`*UnexpectedStatusCodeError`, `*JsonParseError`, `*UnresolvedPromiseError` -
each carry `Code`, `Message`, and `Details`, checkable with `errors.As`:

```go
var badReq *webfunction.BadRequestError
if errors.As(err, &badReq) {
	fmt.Println(badReq.Code, badReq.Message, badReq.Details)
}
```

### Pipelining

```go
client.SetPipeline(webfunction.NewPipeline(client.Package().PipelineURL))
// or: webfunction.FromPackageEndpoint(url, webfunction.Options{Pipelined: true})

userResult, _ := client.Call("find-user", map[string]any{"id": "123"})
user := userResult.(*webfunction.Promise)

orderResult, _ := client.Call("create-order", map[string]any{
	"user_id": user.Field("id"), // references the not-yet-resolved user's id
})
order := orderResult.(*webfunction.Promise)

value, err := order.Resolve() // executes the batched pipeline, fills in both promises
```

**Simplification versus the reference clients**: `Promise.Field`/`Index`
always extend the JSONPath, even after the promise is resolved. Ruby's
`Promise#[]` behaves differently once resolved (indexing into the real,
already-decoded value) - replicating that in Go would need reflection-heavy
navigation of a decoded `any` value, and wasn't implemented. Call `Value()`
or `Resolve()` to get the real value.

## Known gaps / open questions

- **Events**: an earlier Go model (in the wfn CLI's own `webfunction/`
  package) included `Package.EventSourceURL` and an `Event` type. Neither
  exists anywhere in the Ruby reference gem. Whether "events" are a real,
  not-yet-implemented part of the spec, or an earlier over-read of the spec
  site without a reference implementation to check against, is unconfirmed.
  Left out of this package until that's resolved.
- Module path/publishing location not yet decided.

## Development

```
go build ./...
go test ./...
```

No external dependencies - standard library only.