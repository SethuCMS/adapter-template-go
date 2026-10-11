package server

import (
	"bytes"
	"encoding/json"
	"math"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/sethucms/sethucms-adapter-template-go/adapter"
)

// structToMap turns a protobuf Struct into a plain map.
//
// A Struct stores every number as a double, so 3 arrives as 3.0. Whole numbers are turned back
// into ints here. (Whole numbers above 2^53 cannot be represented exactly in a Struct; send those
// as strings.)
func structToMap(s *structpb.Struct) map[string]any {
	if s == nil {
		return map[string]any{}
	}
	return wholeNumbers(s.AsMap()).(map[string]any)
}

func wholeNumbers(v any) any {
	switch x := v.(type) {
	case float64:
		if x == math.Trunc(x) && math.Abs(x) <= 1<<53 {
			return int(x)
		}
		return x
	case map[string]any:
		for k, item := range x {
			x[k] = wholeNumbers(item)
		}
		return x
	case []any:
		for i, item := range x {
			x[i] = wholeNumbers(item)
		}
		return x
	}
	return v
}

// mapToStruct turns a record into a protobuf Struct. Values must be JSON-like (numbers, strings,
// bools, nil, lists, maps, []byte). Convert anything else (times, decimals) in your adapter.
func mapToStruct(m map[string]any) (*structpb.Struct, error) {
	if m == nil {
		m = map[string]any{}
	}
	return structpb.NewStruct(m)
}

// parseQuery turns the portable query (a Struct) into a typed Query. Unknown keys are rejected,
// not ignored: a query the adapter does not fully understand must not run.
func parseQuery(m map[string]any) (adapter.Query, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return adapter.Query{}, adapter.NewError(adapter.ValidationFailed, "The query is not valid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var q adapter.Query
	if err := decoder.Decode(&q); err != nil {
		return adapter.Query{}, adapter.NewError(adapter.ValidationFailed, "The query is not valid")
	}
	return q, nil
}
