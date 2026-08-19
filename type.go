package webfunction

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// Type represents a Web Function type specification - what appears in an
// endpoint's `returns`, or an argument's or attribute's `type`.
// See https://webfunction.org/package#types.
//
// The wire grammar is recursive: a type is a single base/refined type, or a
// union of types written as a JSON array, where an array *entry* that is
// itself an array denotes an array type (whose own entries are, in turn, a
// union of the types its items may take). Type models this directly rather
// than flattening it to a list of strings, so array element types and
// object.<n> references survive parsing.
//
// Ported from the wfn CLI's webfunction/type.go (see the CLI's own header
// comment for why - this package is that eventual replacement), and cross-
// checked against the Ruby reference client's Type::parse/Type::detect.
type Type struct {
	// Union holds every alternative this type may be. A plain (non-union)
	// type has exactly one entry.
	Union []TypeAlt
}

// TypeAlt is one alternative within a Type's union.
type TypeAlt struct {
	// Base is one of: "object", "array", "string", "number", "boolean",
	// "null", or "any".
	Base string

	// Refinement is the dotted suffix narrowing Base, e.g. "email" for
	// "string.email", or the referenced object's name for "object.<n>".
	// Empty when there's no refinement. The "any" base MUST NOT carry one.
	Refinement string

	// Of is the element type, present only when Base == "array" and the
	// wire form was a nested array (e.g. [["string"]] means "array of
	// string"). Nil for a bare "array" entry (array of any).
	Of *Type
}

// IsObjectRef reports whether this alternative is a reference to a named
// object definition (an "object.<n>" refinement).
func (a TypeAlt) IsObjectRef() bool {
	return a.Base == "object" && a.Refinement != ""
}

func (a TypeAlt) String() string {
	s := a.Base
	if a.Refinement != "" {
		s += "." + a.Refinement
	}
	if a.Base == "array" && a.Of != nil {
		s += "<" + a.Of.String() + ">"
	}
	return s
}

// HasBase reports whether any alternative in the union has the given base
// type, e.g. t.HasBase("object").
func (t Type) HasBase(base string) bool {
	for _, alt := range t.Union {
		if alt.Base == base {
			return true
		}
	}
	return false
}

// HasBareArray reports whether the union includes an "array" alternative
// with no nested element type (i.e. array of any, at the wire level) -
// distinct from HasBase("array"), which also matches a typed array.
func (t Type) HasBareArray() bool {
	for _, alt := range t.Union {
		if alt.Base == "array" && alt.Of == nil {
			return true
		}
	}
	return false
}

// ObjectNames returns the names of every object definition referenced
// anywhere within this type, including inside array element types -
// mirrors the Ruby reference's Type#objects.
func (t Type) ObjectNames() []string {
	var names []string
	for _, alt := range t.Union {
		if alt.IsObjectRef() {
			names = append(names, alt.Refinement)
		}
		if alt.Of != nil {
			names = append(names, alt.Of.ObjectNames()...)
		}
	}
	return names
}

func (t Type) String() string {
	parts := make([]string, len(t.Union))
	for i, alt := range t.Union {
		parts[i] = alt.String()
	}
	return strings.Join(parts, "|")
}

// Valid reports whether value conforms to this type: at least one
// alternative in the union must accept it. Mirrors the Ruby reference's
// Type#valid?.
//
// Values are expected in their decoded-JSON form (map[string]any for
// object, []any for array, float64 for number, string, bool, or nil) -
// the shape you get back from encoding/json's default unmarshal into
// `any`.
func (t Type) Valid(value any) bool {
	for _, alt := range t.Union {
		if alt.valid(value) {
			return true
		}
	}
	return false
}

func (a TypeAlt) valid(value any) bool {
	switch a.Base {
	case "any":
		return true
	case "string":
		s, ok := value.(string)
		if !ok {
			return false
		}
		return a.refinementValid(s)
	case "number":
		n, ok := toFloat64(value)
		if !ok {
			return false
		}
		return a.refinementValid(n)
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	case "array":
		arr, ok := value.([]any)
		if !ok {
			return false
		}
		if a.Of == nil {
			return true
		}
		for _, elem := range arr {
			if !a.Of.Valid(elem) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func toFloat64(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	default:
		return 0, false
	}
}

// refinementValid checks value against this alternative's refinement, if
// any. A base type with no refinement (or an unrecognized one) always
// passes here - the caller is responsible for the base-type check itself.
func (a TypeAlt) refinementValid(value any) bool {
	if a.Refinement == "" {
		return true
	}
	validator, ok := refinementValidators[a.Refinement]
	if !ok {
		return true
	}
	return validator(value)
}

// AllowedRefinements lists the refinements recognized for each base type
// that supports refinement ("string" and "number"). Mirrors the Ruby
// reference's Type::ALLOWED_REFINEMENTS.
var AllowedRefinements = map[string][]string{
	"number": {"u32", "u64", "i32", "i64", "f32", "f64", "timestamp"},
	"string": {"date", "time", "datetime", "uuid", "base64", "email", "phone", "url", "uri", "ipv4", "ipv6", "hostname"},
}

// refinementValidators holds a value-level validator per refinement name,
// ported from the Ruby reference's Type::REFINEMENT_VALIDATORS. Refinement
// names are unique across base types, so a flat map (keyed by refinement,
// not base+refinement) is sufficient, matching the Ruby original.
var refinementValidators = map[string]func(value any) bool{
	"u32": func(v any) bool { n, ok := toFloat64(v); return ok && isInt(n) && n >= 0 && n <= 0xFFFFFFFF },
	"u64": func(v any) bool { n, ok := toFloat64(v); return ok && isInt(n) && n >= 0 && n <= 0xFFFFFFFFFFFFFFFF },
	"i32": func(v any) bool { n, ok := toFloat64(v); return ok && isInt(n) && n >= -0x80000000 && n <= 0x7FFFFFFF },
	"i64": func(v any) bool {
		n, ok := toFloat64(v)
		return ok && isInt(n) && n >= -0x8000000000000000 && n <= 0x7FFFFFFFFFFFFFFF
	},
	"f32":       func(v any) bool { n, ok := toFloat64(v); return ok && !isInfOrNaN(n) },
	"f64":       func(v any) bool { n, ok := toFloat64(v); return ok && !isInfOrNaN(n) },
	"timestamp": func(v any) bool { n, ok := toFloat64(v); return ok && isInt(n) && n >= 0 },
	"date":      regexValidator(regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)),
	"time":      regexValidator(regexp.MustCompile(`^\d{2}:\d{2}:\d{2}(\.\d+)?$`)),
	"datetime":  regexValidator(regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})?$`)),
	"uuid":      regexValidator(regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)),
	"base64":    regexValidator(regexp.MustCompile(`^[A-Za-z0-9+/]*={0,2}$`)),
	"email":     regexValidator(regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)),
	"phone":     regexValidator(regexp.MustCompile(`^\+[1-9]\d{1,14}$`)),
	"hostname": regexValidator(regexp.MustCompile(
		`^(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)),
	"url": func(v any) bool {
		s, ok := v.(string)
		if !ok {
			return false
		}
		u, err := url.Parse(s)
		return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
	},
	"uri": func(v any) bool {
		s, ok := v.(string)
		if !ok {
			return false
		}
		u, err := url.Parse(s)
		return err == nil && u.Scheme != ""
	},
	"ipv4": func(v any) bool {
		s, ok := v.(string)
		if !ok {
			return false
		}
		ip := net.ParseIP(s)
		return ip != nil && ip.To4() != nil
	},
	"ipv6": func(v any) bool {
		s, ok := v.(string)
		if !ok {
			return false
		}
		ip := net.ParseIP(s)
		return ip != nil && ip.To4() == nil
	},
}

func regexValidator(re *regexp.Regexp) func(any) bool {
	return func(v any) bool {
		s, ok := v.(string)
		return ok && re.MatchString(s)
	}
}

func isInt(n float64) bool { return n == float64(int64(n)) }

func isInfOrNaN(n float64) bool {
	return n != n || n > 1.7976931348623157e+308 || n < -1.7976931348623157e+308
}

func (t Type) format(f string) string {
	parts := make([]string, len(t.Union))
	for i, alt := range t.Union {
		parts[i] = alt.format(f)
	}
	return strings.Join(parts, " | ")
}

// Format renders the type using one of three styles, mirroring the Ruby
// reference's Type#format(format):
//   - "default": the full dotted form, e.g. "string.email"
//   - "compact": refinement alone if present, else the base, e.g. "email"
//   - "base":    the base type alone, e.g. "string"
func (t Type) Format(style string) string {
	return t.format(style)
}

func (a TypeAlt) format(style string) string {
	switch style {
	case "compact":
		if a.Refinement != "" {
			return a.Refinement
		}
		return a.Base
	case "base":
		return a.Base
	default:
		s := a.Base
		if a.Refinement != "" {
			s += "." + a.Refinement
		}
		if a.Base == "array" && a.Of != nil {
			s += "<" + a.Of.format(style) + ">"
		}
		return s
	}
}

// UnmarshalJSON parses the recursive type grammar: a JSON null (treated as
// "any" - only actually expected for choices/values elements, not type
// fields themselves, but handled defensively), a bare string, or a JSON
// array whose entries are either strings or nested arrays.
func (t *Type) UnmarshalJSON(data []byte) error {
	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	parsed, err := parseTypeValue(raw)
	if err != nil {
		return err
	}
	*t = parsed
	return nil
}

func parseTypeValue(raw interface{}) (Type, error) {
	switch v := raw.(type) {
	case nil:
		return Type{Union: []TypeAlt{{Base: "any"}}}, nil
	case string:
		return Type{Union: []TypeAlt{parseTypeString(v)}}, nil
	case []interface{}:
		if len(v) == 0 {
			// Not permitted per spec ("Arrays MUST NOT be empty at any
			// depth"), but fail soft to "any" rather than erroring
			// outright on a non-conformant package.
			return Type{Union: []TypeAlt{{Base: "any"}}}, nil
		}
		alts := make([]TypeAlt, 0, len(v))
		for _, elem := range v {
			switch e := elem.(type) {
			case string:
				alts = append(alts, parseTypeString(e))
			case []interface{}:
				inner, err := parseTypeValue(e)
				if err != nil {
					return Type{}, err
				}
				alts = append(alts, TypeAlt{Base: "array", Of: &inner})
			default:
				return Type{}, fmt.Errorf("unexpected type entry %T", elem)
			}
		}
		return Type{Union: alts}, nil
	default:
		return Type{}, fmt.Errorf("unexpected type value %T", raw)
	}
}

func parseTypeString(s string) TypeAlt {
	if s == "array" {
		return TypeAlt{Base: "array"}
	}
	if i := strings.IndexByte(s, '.'); i >= 0 {
		base := s[:i]
		refinement := s[i+1:]
		if base == "string" || base == "number" {
			if !containsStr(AllowedRefinements[base], refinement) {
				return TypeAlt{Base: base}
			}
		}
		return TypeAlt{Base: base, Refinement: refinement}
	}
	return TypeAlt{Base: s}
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}