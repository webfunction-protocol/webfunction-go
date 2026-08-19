package webfunction

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"testing"
)

func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	var m map[string]any
	if len(b) > 0 {
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("decoding body: %v", err)
		}
	}
	return m
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encoding response: %v", err)
	}
}

// resolveStepRefs is a deliberately minimal stand-in for what a real
// pipeline server does: it resolves "$[n].field"-shaped string values in
// a step's body against already-computed results from earlier steps in
// the same batch, mutating body in place. Only single-field-deep
// references are handled - enough to exercise Promise.Field in tests,
// not a general JSONPath implementation.
func resolveStepRefs(body map[string]any, results []any) {
	re := regexp.MustCompile(`^\$\[(\d+)\]\.(\w+)$`)
	for k, v := range body {
		s, ok := v.(string)
		if !ok {
			continue
		}
		m := re.FindStringSubmatch(s)
		if m == nil {
			continue
		}
		idx, err := strconv.Atoi(m[1])
		if err != nil || idx < 0 || idx >= len(results) {
			continue
		}
		if obj, ok := results[idx].(map[string]any); ok {
			body[k] = obj[m[2]]
		}
	}
}

func TestFromPackageEndpointAndCall(t *testing.T) {
	mux := http.NewServeMux()
	var baseURL string

	mux.HandleFunc("/package", func(w http.ResponseWriter, r *http.Request) {
		pkg := map[string]any{
			"base_url": baseURL,
			"endpoints": []any{
				map[string]any{
					"name":    "find-user",
					"returns": "object",
					"arguments": []any{
						map[string]any{"name": "id", "type": "string", "flags": []any{"required"}},
					},
				},
				map[string]any{
					"name":    "list-people",
					"returns": []any{[]any{"object"}},
					"flags":   []any{"paginated"},
				},
			},
		}
		writeJSON(t, w, http.StatusOK, pkg)
	})
	mux.HandleFunc("/find-user", func(w http.ResponseWriter, r *http.Request) {
		args := decodeBody(t, r)
		id, _ := args["id"].(string)
		if id == "missing" {
			writeJSON(t, w, http.StatusBadRequest, []any{"USER_NOT_FOUND", "No user with that id.", map[string]any{"id": id}})
			return
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"id": id, "name": "Ada"})
	})
	mux.HandleFunc("/list-people", func(w http.ResponseWriter, r *http.Request) {
		args := decodeBody(t, r)
		if args["cursor"] == "page2" {
			writeJSON(t, w, http.StatusOK, map[string]any{
				"page":     []any{map[string]any{"person_id": "p2"}},
				"next":     nil,
				"previous": map[string]any{"cursor": "page1"},
			})
			return
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"page":     []any{map[string]any{"person_id": "p1"}},
			"next":     map[string]any{"cursor": "page2"},
			"previous": nil,
		})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()
	baseURL = ts.URL + "/"

	client, err := FromPackageEndpoint(ts.URL+"/package", Options{})
	if err != nil {
		t.Fatalf("FromPackageEndpoint: %v", err)
	}

	if got := len(client.Package().Endpoints); got != 2 {
		t.Fatalf("expected 2 endpoints, got %d", got)
	}

	t.Run("plain call", func(t *testing.T) {
		result, err := client.Call("find-user", map[string]any{"id": "123"})
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		obj, ok := result.(map[string]any)
		if !ok {
			t.Fatalf("expected map result, got %T", result)
		}
		if obj["name"] != "Ada" {
			t.Fatalf("expected name Ada, got %v", obj["name"])
		}
	})

	t.Run("underscore name maps to hyphenated endpoint", func(t *testing.T) {
		result, err := client.Call("find_user", map[string]any{"id": "123"})
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		if result.(map[string]any)["id"] != "123" {
			t.Fatalf("unexpected result: %v", result)
		}
	})

	t.Run("bad request error triple", func(t *testing.T) {
		_, err := client.Call("find-user", map[string]any{"id": "missing"})
		if err == nil {
			t.Fatalf("expected an error")
		}
		var badReq *BadRequestError
		if !asBadRequest(err, &badReq) {
			t.Fatalf("expected *BadRequestError, got %T: %v", err, err)
		}
		if badReq.Code != "USER_NOT_FOUND" {
			t.Fatalf("expected code USER_NOT_FOUND, got %s", badReq.Code)
		}
	})

	t.Run("pagination via paginated flag", func(t *testing.T) {
		result, err := client.Call("list-people", nil)
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		page, ok := result.(*Page)
		if !ok {
			t.Fatalf("expected *Page, got %T", result)
		}
		if len(page.Items()) != 1 {
			t.Fatalf("expected 1 item, got %d", len(page.Items()))
		}
		if !page.HasNext() || page.HasPrevious() {
			t.Fatalf("expected HasNext=true HasPrevious=false, got %v %v", page.HasNext(), page.HasPrevious())
		}

		next, err := page.NextPage()
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}
		if next == nil {
			t.Fatal("expected a next page")
		}
		if next.HasNext() || !next.HasPrevious() {
			t.Fatalf("expected HasNext=false HasPrevious=true on page 2, got %v %v", next.HasNext(), next.HasPrevious())
		}

		prev, err := next.PreviousPage()
		if err != nil {
			t.Fatalf("PreviousPage: %v", err)
		}
		if prev == nil {
			t.Fatal("expected to navigate back to page 1")
		}
	})
}

func TestPipelining(t *testing.T) {
	mux := http.NewServeMux()
	var baseURL string

	mux.HandleFunc("/package", func(w http.ResponseWriter, r *http.Request) {
		pkg := map[string]any{
			"base_url":     baseURL,
			"pipeline_url": baseURL + "run-pipeline",
			"endpoints": []any{
				map[string]any{"name": "find-user", "returns": "object", "arguments": []any{
					map[string]any{"name": "id", "type": "string", "flags": []any{"required"}},
				}},
				map[string]any{"name": "create-order", "returns": "object", "arguments": []any{
					map[string]any{"name": "user_id", "type": "string", "flags": []any{"required"}},
				}},
			},
		}
		writeJSON(t, w, http.StatusOK, pkg)
	})

	mux.HandleFunc("/run-pipeline", func(w http.ResponseWriter, r *http.Request) {
		body := decodeBody(t, r)
		steps, _ := body["steps"].([]any)

		results := make([]any, 0, len(steps))
		for _, s := range steps {
			step, _ := s.(map[string]any)
			url, _ := step["url"].(string)
			stepBody, _ := step["body"].(map[string]any)
			resolveStepRefs(stepBody, results)

			switch {
			case len(url) >= len("/find-user") && url[len(url)-len("/find-user"):] == "/find-user":
				results = append(results, map[string]any{"id": "123", "name": "Ada"})
			case len(url) >= len("/create-order") && url[len(url)-len("/create-order"):] == "/create-order":
				results = append(results, map[string]any{"id": "order-1", "user_id": stepBody["user_id"]})
			default:
				results = append(results, nil)
			}
		}
		writeJSON(t, w, http.StatusOK, results)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()
	baseURL = ts.URL + "/"

	client, err := FromPackageEndpoint(ts.URL+"/package", Options{Pipelined: true})
	if err != nil {
		t.Fatalf("FromPackageEndpoint: %v", err)
	}

	userResult, err := client.Call("find-user", map[string]any{"id": "123"})
	if err != nil {
		t.Fatalf("Call find-user: %v", err)
	}
	userPromise, ok := userResult.(*Promise)
	if !ok {
		t.Fatalf("expected *Promise, got %T", userResult)
	}

	orderResult, err := client.Call("create-order", map[string]any{"user_id": userPromise.Field("id")})
	if err != nil {
		t.Fatalf("Call create-order: %v", err)
	}
	orderPromise, ok := orderResult.(*Promise)
	if !ok {
		t.Fatalf("expected *Promise, got %T", orderResult)
	}

	resolved, err := orderPromise.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	order, ok := resolved.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", resolved)
	}
	if order["user_id"] != "123" {
		t.Fatalf("expected user_id to resolve to 123, got %v", order["user_id"])
	}

	// Once the pipeline has run, the first promise should be resolved too.
	if _, err := userPromise.Value(); err != nil {
		t.Fatalf("expected userPromise to be resolved after pipeline execution: %v", err)
	}
}

func TestUnresolvedPromiseError(t *testing.T) {
	pl := NewPipeline("https://example.com/run-pipeline")
	promise := pl.AddStep("https://example.com/find-user", map[string]string{}, map[string]any{"id": "1"})

	if _, err := promise.Value(); err == nil {
		t.Fatal("expected an error before the pipeline has executed")
	} else {
		var unresolved *UnresolvedPromiseError
		if !asUnresolved(err, &unresolved) {
			t.Fatalf("expected *UnresolvedPromiseError, got %T: %v", err, err)
		}
	}
}

func TestTypeValidation(t *testing.T) {
	emailType := Type{Union: []TypeAlt{{Base: "string", Refinement: "email"}}}
	if !emailType.Valid("ada@example.com") {
		t.Error("expected a valid email to pass")
	}
	if emailType.Valid("not-an-email") {
		t.Error("expected an invalid email to fail")
	}

	u32Type := Type{Union: []TypeAlt{{Base: "number", Refinement: "u32"}}}
	if !u32Type.Valid(float64(42)) {
		t.Error("expected 42 to be a valid u32")
	}
	if u32Type.Valid(float64(-1)) {
		t.Error("expected -1 to be an invalid u32")
	}

	arrType := Type{Union: []TypeAlt{{Base: "array", Of: &Type{Union: []TypeAlt{{Base: "string"}}}}}}
	if !arrType.Valid([]any{"a", "b"}) {
		t.Error("expected a string array to be valid")
	}
	if arrType.Valid([]any{"a", 1}) {
		t.Error("expected a mixed array to be invalid")
	}
}

func TestFromURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/package.json", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("api_version"); got != "2" {
			t.Errorf("expected api_version=2 query param, got %q", got)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"base_url":  "https://example.com/",
			"endpoints": []any{map[string]any{"name": "ping", "returns": "boolean"}},
		})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client, err := FromURL(ts.URL+"/package.json", Options{Version: "2"})
	if err != nil {
		t.Fatalf("FromURL: %v", err)
	}
	if len(client.Package().Endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(client.Package().Endpoints))
	}
	if client.Package().BaseURL != "https://example.com/" {
		t.Fatalf("unexpected base_url: %s", client.Package().BaseURL)
	}
}

func TestObjectInContext(t *testing.T) {
	pkg := &Package{
		Objects: []Object{
			{
				Name:       "user",
				Arguments:  []Argument{{Name: "id", Type: Type{Union: []TypeAlt{{Base: "string"}}}}},
				Attributes: []Attribute{{Name: "id", Type: Type{Union: []TypeAlt{{Base: "string"}}}}, {Name: "name", Type: Type{Union: []TypeAlt{{Base: "string"}}}}},
			},
			{
				Name:      "argument-only",
				Arguments: []Argument{{Name: "x", Type: Type{Union: []TypeAlt{{Base: "string"}}}}},
			},
		},
	}

	if obj := pkg.ObjectInContext("user", ArgumentContext); obj == nil || len(obj.Arguments) != 1 {
		t.Fatal("expected user object to resolve in argument context")
	}
	if obj := pkg.ObjectInContext("user", AttributeContext); obj == nil || len(obj.Attributes) != 2 {
		t.Fatal("expected user object to resolve in attribute context")
	}
	if obj := pkg.ObjectInContext("argument-only", AttributeContext); obj != nil {
		t.Fatal("expected argument-only object to resolve to nil in attribute context")
	}
	if obj := pkg.ObjectInContext("does-not-exist", ArgumentContext); obj != nil {
		t.Fatal("expected unknown object to resolve to nil")
	}
}

// asBadRequest / asUnresolved are tiny errors.As wrappers, avoiding an
// import of the standard "errors" package purely for this test file's
// convenience.
func asBadRequest(err error, target **BadRequestError) bool {
	if e, ok := err.(*BadRequestError); ok {
		*target = e
		return true
	}
	return false
}

func asUnresolved(err error, target **UnresolvedPromiseError) bool {
	if e, ok := err.(*UnresolvedPromiseError); ok {
		*target = e
		return true
	}
	return false
}