package adapter

import (
	"context"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Memory is a complete, working adapter that keeps records in memory.
//
// Read this first, then copy it and replace the storage with your database driver. It shows
// the shape of every method and the safety checks that go with them.
type Memory struct {
	mu       sync.Mutex
	rows     map[int]Record
	nextID   int
	readOnly bool
	open     bool
}

// NewMemory creates an adapter. Use it as the factory: server.New(func() adapter.Adapter { return adapter.NewMemory() }, "memory").
func NewMemory() *Memory { return &Memory{rows: map[int]Record{}, nextID: 1, readOnly: true} }

// What this example serves. A real adapter reads this from the database (introspection).
var memoryFields = map[string]FieldInfo{
	"id":     {NativeName: "id", NativeType: "int", CanonicalType: "integer", Required: true, IsPrimaryKey: true},
	"title":  {NativeName: "title", NativeType: "text", CanonicalType: "string", Required: true},
	"score":  {NativeName: "score", NativeType: "int", CanonicalType: "integer"},
	"status": {NativeName: "status", NativeType: "text", CanonicalType: "string"},
}

const memoryCollection = "items"

// Connect opens the connection.
// A real adapter opens a pooled connection here using in.Credentials, and maps driver errors to
// ConnectionFailed / AuthFailed. Never include the driver's message in the error.
func (m *Memory) Connect(_ context.Context, in ConnectInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readOnly = in.ReadOnly
	m.open = true
	return nil
}

func (m *Memory) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.open = false
	return nil
}

func (m *Memory) Capabilities() Capabilities {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Capabilities{
		Transactions: "none", FilterOr: true, FilterInequality: "full", Sort: "any-field",
		OffsetPagination: true, Schema: "enforced", Writes: !m.readOnly,
	}
}

func (m *Memory) Introspect(_ context.Context) ([]CollectionInfo, error) {
	fields := []FieldInfo{memoryFields["id"], memoryFields["title"], memoryFields["score"], memoryFields["status"]}
	return []CollectionInfo{{NativeName: memoryCollection, Fields: fields}}, nil
}

// Find runs a portable query.
func (m *Memory) Find(ctx context.Context, q Query) (FindResult, error) {
	if err := ctx.Err(); err != nil {
		return FindResult{}, NewError(Timeout, "The request was cancelled")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.requireCollection(q.Collection); err != nil {
		return FindResult{}, err
	}
	// Validate everything up front, even when there are no rows to test it on.
	if err := checkFilter(q.Filter); err != nil {
		return FindResult{}, err
	}
	for _, s := range q.Sort {
		if _, ok := memoryFields[s.Field]; !ok {
			return FindResult{}, NewError(ValidationFailed, "Unknown field")
		}
		if s.Dir != "" && s.Dir != "asc" && s.Dir != "desc" {
			return FindResult{}, NewError(ValidationFailed, "Sort direction must be asc or desc")
		}
	}
	for _, name := range q.Projection {
		if _, ok := memoryFields[name]; !ok {
			return FindResult{}, NewError(ValidationFailed, "Unknown field")
		}
	}
	limit := q.Page.Limit
	if limit <= 0 {
		limit = 50
	}
	offset, err := readCursor(q.Page.Cursor)
	if err != nil {
		return FindResult{}, err
	}

	rows := make([]Record, 0, len(m.rows))
	for _, row := range m.rows {
		if matches(q.Filter, row) {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i]["id"].(int) < rows[j]["id"].(int) }) // stable tie-break
	sort.SliceStable(rows, func(i, j int) bool {
		for _, s := range q.Sort {
			c := compareValues(rows[i][s.Field], rows[j][s.Field])
			if c == 0 {
				continue
			}
			if s.Dir == "desc" {
				return c > 0
			}
			return c < 0
		}
		return false
	})

	end := offset + limit
	more := end < len(rows)
	if offset > len(rows) {
		offset = len(rows)
	}
	if end > len(rows) {
		end = len(rows)
	}
	out := make([]Record, 0, end-offset)
	for _, row := range rows[offset:end] {
		out = append(out, project(row, q.Projection))
	}
	next := ""
	if more {
		next = strconv.Itoa(end)
	}
	return FindResult{Records: out, NextCursor: next}, nil
}

func (m *Memory) Get(_ context.Context, collection, id string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.requireCollection(collection); err != nil {
		return nil, err
	}
	row, ok := m.rows[parseID(id)]
	if !ok {
		return nil, NewError(NotFound, "No such record")
	}
	return copyRecord(row), nil
}

func (m *Memory) Create(_ context.Context, collection string, values Record) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.requireWritable(collection); err != nil {
		return nil, err
	}
	record, err := validated(values, true)
	if err != nil {
		return nil, err
	}
	record["id"] = m.nextID
	m.nextID++
	m.rows[record["id"].(int)] = record
	return copyRecord(record), nil
}

func (m *Memory) Update(_ context.Context, collection, id string, values Record) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.requireWritable(collection); err != nil {
		return nil, err
	}
	row, ok := m.rows[parseID(id)]
	if !ok {
		return nil, NewError(NotFound, "No such record")
	}
	changes, err := validated(values, false)
	if err != nil {
		return nil, err
	}
	for k, v := range changes {
		row[k] = v
	}
	return copyRecord(row), nil
}

func (m *Memory) Delete(_ context.Context, collection, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.requireWritable(collection); err != nil {
		return err
	}
	key := parseID(id)
	if _, ok := m.rows[key]; !ok {
		return NewError(NotFound, "No such record")
	}
	delete(m.rows, key)
	return nil
}

// ---- checks (every adapter needs the equivalents) -------------------------------------

func (m *Memory) requireCollection(name string) error {
	if !m.open {
		return NewError(ConnectionFailed, "The connection is closed")
	}
	if name != memoryCollection {
		return NewError(ValidationFailed, "Unknown collection")
	}
	return nil
}

func (m *Memory) requireWritable(collection string) error {
	if err := m.requireCollection(collection); err != nil {
		return err
	}
	if m.readOnly {
		return NewError(ReadOnly, "This connection is read-only")
	}
	return nil
}

func validated(values Record, creating bool) (Record, error) {
	clean := Record{}
	for name, value := range values {
		if _, ok := memoryFields[name]; !ok {
			return nil, NewError(ValidationFailed, "Unknown field")
		}
		if name == "id" {
			return nil, NewError(ValidationFailed, "The id is generated and cannot be set")
		}
		clean[name] = value
	}
	if title, present := clean["title"]; present {
		s, ok := title.(string)
		if !ok || s == "" || len(s) > 200 {
			return nil, NewError(ValidationFailed, "title must be text of 1 to 200 characters")
		}
	} else if creating {
		return nil, NewError(ValidationFailed, "title is required")
	}
	if score, present := clean["score"]; present && score != nil {
		n, ok := wholeNumber(score)
		if !ok {
			return nil, NewError(ValidationFailed, "score must be a whole number")
		}
		clean["score"] = n
	}
	if creating {
		if _, ok := clean["score"]; !ok {
			clean["score"] = nil
		}
		if _, ok := clean["status"]; !ok {
			clean["status"] = nil
		}
	}
	return clean, nil
}

// wholeNumber accepts ints and whole floats (a JSON number arrives as float64).
func wholeNumber(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		if n == math.Trunc(n) && math.Abs(n) <= 1<<53 {
			return int(n), true
		}
	}
	return 0, false
}

func parseID(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return -1 // a malformed id is simply not found
	}
	return n
}

func readCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(cursor)
	if err != nil || n < 0 {
		return 0, NewError(ValidationFailed, "Invalid cursor")
	}
	return n, nil
}

var operators = map[string]bool{"eq": true, "ne": true, "lt": true, "lte": true, "gt": true, "gte": true,
	"in": true, "nin": true, "contains": true, "startsWith": true, "exists": true}

// checkFilter refuses unknown fields and operators before touching any data.
func checkFilter(f *Filter) error {
	if f == nil {
		return nil
	}
	switch {
	case len(f.And) > 0:
		for i := range f.And {
			if err := checkFilter(&f.And[i]); err != nil {
				return err
			}
		}
		return nil
	case len(f.Or) > 0:
		for i := range f.Or {
			if err := checkFilter(&f.Or[i]); err != nil {
				return err
			}
		}
		return nil
	case f.Not != nil:
		return checkFilter(f.Not)
	}
	if _, ok := memoryFields[f.Field]; !ok {
		return NewError(ValidationFailed, "Unknown field")
	}
	if !operators[f.Op] {
		return NewError(CapabilityUnsupported, "Unsupported operator")
	}
	return nil
}

func matches(f *Filter, row Record) bool {
	if f == nil {
		return true
	}
	switch {
	case len(f.And) > 0:
		for i := range f.And {
			if !matches(&f.And[i], row) {
				return false
			}
		}
		return true
	case len(f.Or) > 0:
		for i := range f.Or {
			if matches(&f.Or[i], row) {
				return true
			}
		}
		return false
	case f.Not != nil:
		return !matches(f.Not, row)
	}
	actual := row[f.Field]
	switch f.Op {
	case "exists":
		want, _ := f.Value.(bool)
		return (actual != nil) == want
	case "eq":
		return equal(actual, f.Value)
	case "ne":
		return !equal(actual, f.Value)
	case "in", "nin":
		list, _ := f.Value.([]any)
		found := false
		for _, item := range list {
			if equal(actual, item) {
				found = true
				break
			}
		}
		return found == (f.Op == "in")
	case "lt", "lte", "gt", "gte":
		a, aok := toFloat(actual)
		b, bok := toFloat(f.Value)
		if !aok || !bok {
			return false // null or non-numeric never matches an inequality
		}
		switch f.Op {
		case "lt":
			return a < b
		case "lte":
			return a <= b
		case "gt":
			return a > b
		default:
			return a >= b
		}
	case "contains", "startsWith":
		a, aok := actual.(string)
		b, bok := f.Value.(string)
		if !aok || !bok {
			return false
		}
		a, b = strings.ToLower(a), strings.ToLower(b)
		if f.Op == "contains" {
			return strings.Contains(a, b)
		}
		return strings.HasPrefix(a, b)
	}
	return false
}

func equal(a, b any) bool {
	if af, ok := toFloat(a); ok {
		bf, ok := toFloat(b)
		return ok && af == bf
	}
	as, aok := a.(string)
	bs, bok := b.(string)
	if aok || bok {
		return aok && bok && as == bs
	}
	return a == nil && b == nil // other types (maps, lists) never equal a scalar
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

// compareValues orders nil first, then numbers, then text.
func compareValues(a, b any) int {
	rank := func(v any) int {
		if v == nil {
			return 0
		}
		if _, ok := toFloat(v); ok {
			return 1
		}
		return 2
	}
	ra, rb := rank(a), rank(b)
	if ra != rb {
		return ra - rb
	}
	switch ra {
	case 1:
		af, _ := toFloat(a)
		bf, _ := toFloat(b)
		switch {
		case af < bf:
			return -1
		case af > bf:
			return 1
		}
	case 2:
		return strings.Compare(toString(a), toString(b))
	}
	return 0
}

func toString(v any) string {
	s, _ := v.(string)
	return s
}

func project(row Record, projection []string) Record {
	if len(projection) == 0 {
		return copyRecord(row)
	}
	out := Record{}
	for _, name := range projection {
		if v, ok := row[name]; ok {
			out[name] = v
		}
	}
	return out
}

func copyRecord(r Record) Record {
	out := make(Record, len(r))
	for k, v := range r {
		out[k] = v
	}
	return out
}
