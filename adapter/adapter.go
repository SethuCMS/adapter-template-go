// Package adapter is the contract your adapter implements.
//
// You write plain Go against these types. The server package turns them into the gRPC
// protocol, so you never touch protobuf.
//
// Rules every adapter must follow (the gateway relies on them):
//
//  1. Never build queries by pasting text together. Use your driver's placeholders.
//     Treat every field name and value as hostile data.
//  2. Only use names you learned from introspection. Refuse anything else with
//     ValidationFailed.
//  3. Be honest in Capabilities(). If the database cannot do something, say so; the gateway
//     refuses the query. Never approximate silently: an approximate filter returns the wrong
//     rows and nobody notices.
//  4. Never put the database's own error text into an Error message. It can contain host
//     names, user names and SQL. Use a short fixed sentence.
//  5. Never log credentials. They are passed to Connect only and live in memory.
//  6. Honour ReadOnly. When it is true, Create, Update and Delete must fail with ReadOnly,
//     and where the database supports it, open a read-only session too.
//  7. Return at most Query.Page.Limit records, and a NextCursor only when there are more.
//     The gateway signs and verifies cursors; yours can be any string.
//  8. Respect ctx. Stop work when it is cancelled or its deadline passes.
package adapter

import (
	"context"
	"fmt"
)

// Record is one row/document as plain JSON-like data.
type Record = map[string]any

// Code is an error code the gateway understands.
type Code string

const (
	ConnectionFailed      Code = "CONNECTION_FAILED"
	AuthFailed            Code = "AUTH_FAILED"
	NotFound              Code = "NOT_FOUND"
	Conflict              Code = "CONFLICT"
	ValidationFailed      Code = "VALIDATION_FAILED"
	CapabilityUnsupported Code = "CAPABILITY_UNSUPPORTED"
	RateLimited           Code = "RATE_LIMITED"
	Timeout               Code = "TIMEOUT"
	ReadOnly              Code = "READ_ONLY"
	Internal              Code = "INTERNAL"
)

// Error is the error to return for any failure you expect. Anything else the server sees is
// turned into a generic Internal error, so a driver's message can never reach a client.
type Error struct {
	Code    Code
	Message string
	Details map[string]any
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// NewError builds an Error.
func NewError(code Code, message string) *Error { return &Error{Code: code, Message: message} }

// ConnectInput is what Connect receives.
type ConnectInput struct {
	Config      map[string]any
	Credentials map[string]any // in memory only; never log or store
	ReadOnly    bool
}

// FieldInfo describes one field of a collection.
type FieldInfo struct {
	NativeName    string
	NativeType    string
	CanonicalType string // string, integer, number, boolean, datetime, json, bytes, ...
	Required      bool
	IsPrimaryKey  bool
	Lossy         bool // true when the native type cannot be represented exactly
}

// RelationInfo describes a link to another collection.
type RelationInfo struct {
	Name    string
	Target  string
	Kind    string // "one" or "many"
	Mapping map[string]any
}

// CollectionInfo describes one collection (table, document collection, ...).
type CollectionInfo struct {
	NativeName string
	Fields     []FieldInfo
	Relations  []RelationInfo
}

// Capabilities says what this database can do. The zero value is the most restrictive;
// turn things on only when they are true.
type Capabilities struct {
	Joins            bool
	Transactions     string // none | single-document | multi-document
	FilterOr         bool
	FilterInequality string // full | single-field | none
	FullText         bool
	Sort             string // any-field | indexed-only | key-only
	OffsetPagination bool   // cursor pagination is mandatory regardless
	Aggregations     bool
	Realtime         bool
	Schema           string // enforced | inferred
	Writes           bool
}

// Filter is either one comparison (Field, Op, Value) or a combination (And, Or, Not).
type Filter struct {
	And   []Filter `json:"and,omitempty"`
	Or    []Filter `json:"or,omitempty"`
	Not   *Filter  `json:"not,omitempty"`
	Field string   `json:"field,omitempty"`
	Op    string   `json:"op,omitempty"` // eq ne lt lte gt gte in nin contains startsWith exists
	Value any      `json:"value,omitempty"`
}

// SortSpec is one sort key.
type SortSpec struct {
	Field string `json:"field"`
	Dir   string `json:"dir,omitempty"` // "asc" (default) or "desc"
}

// PageSpec asks for one page.
type PageSpec struct {
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"` // empty on the first page
}

// Query is the portable query (see query-ast.schema.json).
type Query struct {
	Collection string     `json:"collection"`
	Filter     *Filter    `json:"filter,omitempty"`
	Sort       []SortSpec `json:"sort,omitempty"`
	Projection []string   `json:"projection,omitempty"`
	Page       PageSpec   `json:"page,omitempty"`
}

// FindResult is one page of records.
type FindResult struct {
	Records    []Record
	NextCursor string // empty means "last page"
	Notes      []string
}

// Adapter is what you implement. The server creates one per connection with your factory.
type Adapter interface {
	// Connect opens the connection. Return ConnectionFailed or AuthFailed on failure.
	Connect(ctx context.Context, in ConnectInput) error
	Capabilities() Capabilities
	// Introspect lists the collections you can safely serve. Skip anything you cannot handle safely.
	Introspect(ctx context.Context) ([]CollectionInfo, error)
	Find(ctx context.Context, q Query) (FindResult, error)
	// Get returns one record by primary key, or a NotFound error.
	Get(ctx context.Context, collection, id string) (Record, error)
	Create(ctx context.Context, collection string, values Record) (Record, error)
	// Update returns NotFound when there is no such record.
	Update(ctx context.Context, collection, id string, values Record) (Record, error)
	// Delete returns NotFound when there is no such record.
	Delete(ctx context.Context, collection, id string) error
	// Close releases the connection. Called once.
	Close() error
}
