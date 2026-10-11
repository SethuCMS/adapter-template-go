package adapter

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func newConnected(t *testing.T, readOnly bool) *Memory {
	t.Helper()
	m := NewMemory()
	if err := m.Connect(context.Background(), ConnectInput{ReadOnly: readOnly}); err != nil {
		t.Fatal(err)
	}
	return m
}

func code(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func create(t *testing.T, m *Memory, v Record) Record {
	t.Helper()
	r, err := m.Create(context.Background(), "items", v)
	if err != nil {
		t.Fatalf("create %v: %v", v, err)
	}
	return r
}

func titles(res FindResult) []string {
	out := []string{}
	for _, r := range res.Records {
		out = append(out, r["title"].(string))
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRoundTrip(t *testing.T) {
	m := newConnected(t, false)
	ctx := context.Background()
	created := create(t, m, Record{"title": "First", "score": 1.0}) // a JSON number arrives as float64
	if created["id"] != 1 || created["score"] != 1 {
		t.Fatalf("unexpected record %v", created)
	}
	got, err := m.Get(ctx, "items", "1")
	if err != nil || got["title"] != "First" {
		t.Fatalf("get: %v %v", got, err)
	}
	updated, err := m.Update(ctx, "items", "1", Record{"score": 5})
	if err != nil || updated["score"] != 5 {
		t.Fatalf("update: %v %v", updated, err)
	}
	if err := m.Delete(ctx, "items", "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(ctx, "items", "1"); code(err) != NotFound {
		t.Fatalf("want NotFound, got %v", err)
	}
	if err := m.Delete(ctx, "items", "1"); code(err) != NotFound {
		t.Fatalf("want NotFound, got %v", err)
	}
}

func TestPaginationReturnsEveryRecordOnce(t *testing.T) {
	m := newConnected(t, false)
	for i := 0; i < 7; i++ {
		create(t, m, Record{"title": fmt.Sprintf("t%d", i), "score": i})
	}
	var seen []string
	cursor := ""
	for page := 0; page < 10; page++ {
		res, err := m.Find(context.Background(), Query{Collection: "items", Page: PageSpec{Limit: 3, Cursor: cursor}})
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, titles(res)...)
		cursor = res.NextCursor
		if cursor == "" {
			break
		}
	}
	want := []string{"t0", "t1", "t2", "t3", "t4", "t5", "t6"}
	if !equalStrings(seen, want) {
		t.Fatalf("got %v want %v", seen, want)
	}
}

func TestFiltersSortingProjection(t *testing.T) {
	m := newConnected(t, false)
	for _, r := range []Record{
		{"title": "a", "score": 3, "status": "x"}, {"title": "b", "score": 1, "status": "y"},
		{"title": "c", "score": 2, "status": "x"}, {"title": "d", "status": "y"},
	} {
		create(t, m, r)
	}
	ctx := context.Background()
	both := &Filter{And: []Filter{{Field: "status", Op: "eq", Value: "x"}, {Field: "score", Op: "gte", Value: 2.0}}}
	res, err := m.Find(ctx, Query{Collection: "items", Filter: both, Sort: []SortSpec{{Field: "score", Dir: "desc"}}})
	if err != nil || !equalStrings(titles(res), []string{"a", "c"}) {
		t.Fatalf("and+sort: %v %v", titles(res), err)
	}
	either := &Filter{Or: []Filter{{Field: "title", Op: "eq", Value: "a"}, {Field: "title", Op: "startsWith", Value: "B"}}}
	res, _ = m.Find(ctx, Query{Collection: "items", Filter: either})
	if !equalStrings(titles(res), []string{"a", "b"}) {
		t.Fatalf("or: %v", titles(res))
	}
	none := &Filter{Not: &Filter{Field: "score", Op: "exists", Value: true}}
	res, _ = m.Find(ctx, Query{Collection: "items", Filter: none})
	if !equalStrings(titles(res), []string{"d"}) {
		t.Fatalf("not exists: %v", titles(res))
	}
	res, _ = m.Find(ctx, Query{Collection: "items", Projection: []string{"title"}, Page: PageSpec{Limit: 1}})
	if len(res.Records[0]) != 1 {
		t.Fatalf("projection: %v", res.Records[0])
	}
}

func TestUnknownNamesAreRefusedNotIgnored(t *testing.T) {
	m := newConnected(t, false) // no rows on purpose: validation must not depend on data
	ctx := context.Background()
	cases := map[string]Query{
		"field":     {Collection: "items", Filter: &Filter{Field: "nope", Op: "eq", Value: 1}},
		"sort":      {Collection: "items", Sort: []SortSpec{{Field: "nope"}}},
		"project":   {Collection: "items", Projection: []string{"nope"}},
		"table":     {Collection: "users"},
		"direction": {Collection: "items", Sort: []SortSpec{{Field: "id", Dir: "sideways"}}},
		"cursor":    {Collection: "items", Page: PageSpec{Cursor: "abc"}},
	}
	for name, q := range cases {
		if _, err := m.Find(ctx, q); code(err) != ValidationFailed {
			t.Errorf("%s: want ValidationFailed, got %v", name, err)
		}
	}
	if _, err := m.Find(ctx, Query{Collection: "items", Filter: &Filter{Field: "title", Op: "regex", Value: ".*"}}); code(err) != CapabilityUnsupported {
		t.Errorf("operator: got %v", err)
	}
	if _, err := m.Create(ctx, "items", Record{"title": "x", "nope": 1}); code(err) != ValidationFailed {
		t.Errorf("create unknown field: got %v", err)
	}
}

func TestHostileValuesAreJustData(t *testing.T) {
	m := newConnected(t, false)
	create(t, m, Record{"title": "'; DROP TABLE items; --"})
	res, _ := m.Find(context.Background(), Query{Collection: "items", Filter: &Filter{Field: "title", Op: "contains", Value: "DROP TABLE"}})
	if len(res.Records) != 1 {
		t.Fatal("expected the hostile text to be stored and found as plain text")
	}
	res, _ = m.Find(context.Background(), Query{Collection: "items", Filter: &Filter{Field: "title", Op: "eq", Value: map[string]any{"$ne": nil}}})
	if len(res.Records) != 0 {
		t.Fatal("an object value must not match")
	}
}

func TestReadOnlyRejectsEveryWrite(t *testing.T) {
	m := newConnected(t, true)
	ctx := context.Background()
	if _, err := m.Create(ctx, "items", Record{"title": "x"}); code(err) != ReadOnly {
		t.Errorf("create: %v", err)
	}
	if _, err := m.Update(ctx, "items", "1", Record{"title": "x"}); code(err) != ReadOnly {
		t.Errorf("update: %v", err)
	}
	if err := m.Delete(ctx, "items", "1"); code(err) != ReadOnly {
		t.Errorf("delete: %v", err)
	}
	if m.Capabilities().Writes {
		t.Error("a read-only connection must not claim writes")
	}
}

func TestWrittenRecordsAreValidated(t *testing.T) {
	m := newConnected(t, false)
	ctx := context.Background()
	bad := []Record{
		{},                                   // title required
		{"title": ""},                        // empty
		{"title": string(make([]byte, 201))}, // too long
		{"title": "x", "id": 5},              // generated
		{"title": "x", "score": "high"},      // wrong type
		{"title": "x", "score": 1.5},         // not whole
	}
	for i, v := range bad {
		if _, err := m.Create(ctx, "items", v); code(err) != ValidationFailed {
			t.Errorf("case %d (%v): got %v", i, v, err)
		}
	}
}

func TestClosedConnectionFailsCleanly(t *testing.T) {
	m := NewMemory()
	if _, err := m.Find(context.Background(), Query{Collection: "items"}); code(err) != ConnectionFailed {
		t.Fatalf("before connect: %v", err)
	}
	m = newConnected(t, false)
	_ = m.Close()
	if _, err := m.Find(context.Background(), Query{Collection: "items"}); code(err) != ConnectionFailed {
		t.Fatalf("after close: %v", err)
	}
}

func TestCancelledContextStopsWork(t *testing.T) {
	m := newConnected(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Find(ctx, Query{Collection: "items"}); code(err) != Timeout {
		t.Fatalf("got %v", err)
	}
}

func TestMalformedIDIsNotFound(t *testing.T) {
	m := newConnected(t, false)
	if _, err := m.Get(context.Background(), "items", "1; DROP"); code(err) != NotFound {
		t.Fatalf("got %v", err)
	}
}
