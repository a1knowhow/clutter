package cuttle

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

func decodeJSON(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	return d.Decode(out)
}

// normalizeNumbers preserves signed 64-bit integers, including those above 2^53.
func normalizeNumbers(value any) (any, error) {
	switch v := value.(type) {
	case json.Number:
		if !strings.ContainsAny(string(v), ".eE") {
			n, e := v.Int64()
			if e != nil {
				return nil, fmt.Errorf("%w: integer outside signed 64-bit range", ErrInvalid)
			}
			return n, nil
		}
		n, e := strconv.ParseFloat(string(v), 64)
		if e != nil || math.IsInf(n, 0) || math.IsNaN(n) {
			return nil, ErrInvalid
		}
		return n, nil
	case map[string]any:
		for k, x := range v {
			n, e := normalizeNumbers(x)
			if e != nil {
				return nil, e
			}
			v[k] = n
		}
		return v, nil
	case Properties:
		for k, x := range v {
			n, e := normalizeNumbers(x)
			if e != nil {
				return nil, e
			}
			v[k] = n
		}
		return v, nil
	case []any:
		for i, x := range v {
			n, e := normalizeNumbers(x)
			if e != nil {
				return nil, e
			}
			v[i] = n
		}
		return v, nil
	}
	return value, nil
}
func normalizeVersions(st *state) error {
	for _, versions := range [][]Version{st.Current, st.History} {
		for _, v := range versions {
			if v.Node != nil {
				if _, e := normalizeNumbers(v.Node.Properties); e != nil {
					return e
				}
			} else if v.Relationship != nil {
				if _, e := normalizeNumbers(v.Relationship.Properties); e != nil {
					return e
				}
			}
		}
	}
	return nil
}

func textValid(s string) bool { return utf8.ValidString(s) && !strings.ContainsRune(s, 0) }
func validateJSONText(v any, depth int) error {
	if depth > 64 {
		return fmt.Errorf("%w: property nesting exceeds 64", ErrInvalid)
	}
	return validateReflectText(reflect.ValueOf(v), depth)
}
func validateReflectText(v reflect.Value, depth int) error {
	if depth > 64 {
		return fmt.Errorf("%w: property nesting exceeds 64", ErrInvalid)
	}
	if !v.IsValid() {
		return nil
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if !v.IsNil() {
			return validateReflectText(v.Elem(), depth+1)
		}
	case reflect.String:
		if !textValid(v.String()) {
			return fmt.Errorf("%w: text contains an invalid character", ErrInvalid)
		}
	case reflect.Map:
		for iter := v.MapRange(); iter.Next(); {
			if iter.Key().Kind() == reflect.String && iter.Key().String() == "__cuttle_entity_kind" {
				return fmt.Errorf("%w: reserved internal map key", ErrInvalid)
			}
			if e := validateReflectText(iter.Key(), depth+1); e != nil {
				return e
			}
			if e := validateReflectText(iter.Value(), depth+1); e != nil {
				return e
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if e := validateReflectText(v.Index(i), depth+1); e != nil {
				return e
			}
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				if e := validateReflectText(v.Field(i), depth+1); e != nil {
					return e
				}
			}
		}
	}
	return nil
}
func validateEvidence(es []Evidence, p Properties) error {
	for _, e := range es {
		if e.Source == "" || e.Revision == "" || !textValid(e.Source) || !textValid(e.Revision) || !textValid(e.Locator) || !textValid(e.Property) || !textValid(e.Activity) {
			return fmt.Errorf("%w: evidence requires source and revision with valid text", ErrInvalid)
		}
		if e.Property != "" {
			if _, ok := p[e.Property]; !ok {
				return fmt.Errorf("%w: evidence references absent property", ErrInvalid)
			}
		}
	}
	return nil
}
