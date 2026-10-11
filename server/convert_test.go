package server

import (
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/sethucms/sethucms-adapter-template-go/adapter"
)

func TestStructNumbersComeBackAsInts(t *testing.T) {
	s, err := structpb.NewStruct(map[string]any{"a": 3.0, "b": 1.5, "nested": map[string]any{"c": 7.0}, "list": []any{2.0, "x"}})
	if err != nil {
		t.Fatal(err)
	}
	m := structToMap(s)
	if m["a"] != 3 || m["b"] != 1.5 {
		t.Fatalf("got %#v", m)
	}
	if m["nested"].(map[string]any)["c"] != 7 || m["list"].([]any)[0] != 2 {
		t.Fatalf("nested numbers not converted: %#v", m)
	}
	if got := structToMap(nil); len(got) != 0 {
		t.Fatalf("nil struct should be empty, got %#v", got)
	}
}

func TestParseQueryReadsAPortableQuery(t *testing.T) {
	q, err := parseQuery(map[string]any{
		"collection": "items",
		"filter":     map[string]any{"and": []any{map[string]any{"field": "a", "op": "eq", "value": 1}}},
		"sort":       []any{map[string]any{"field": "a", "dir": "desc"}},
		"projection": []any{"a"},
		"page":       map[string]any{"limit": 20, "cursor": "5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if q.Collection != "items" || q.Page.Limit != 20 || q.Page.Cursor != "5" || q.Sort[0].Dir != "desc" || q.Filter.And[0].Op != "eq" {
		t.Fatalf("unexpected query %#v", q)
	}
}

func TestParseQueryRejectsWhatItDoesNotUnderstand(t *testing.T) {
	bad := []map[string]any{
		{"collection": "items", "unexpected": true},                                // unknown key
		{"collection": "items", "page": map[string]any{"limit": 1.5}},              // not a whole number
		{"collection": "items", "filter": map[string]any{"field": "a", "nope": 1}}, // unknown filter key
		{"collection": 7}, // wrong type
	}
	for i, m := range bad {
		_, err := parseQuery(m)
		var e *adapter.Error
		if err == nil {
			t.Errorf("case %d: expected an error", i)
			continue
		}
		e, _ = err.(*adapter.Error)
		if e == nil || e.Code != adapter.ValidationFailed {
			t.Errorf("case %d: want ValidationFailed, got %v", i, err)
		}
	}
}
