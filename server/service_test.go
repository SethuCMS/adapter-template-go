package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/sethucms/sethucms-adapter-template-go/adapter"
	pb "github.com/sethucms/sethucms-adapter-template-go/gen/adapterv1"
)

func newService() *Service {
	return New(func() adapter.Adapter { return adapter.NewMemory() }, "memory", nil)
}

func mustStruct(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func connect(t *testing.T, s *Service, readOnly bool) {
	t.Helper()
	res, err := s.Connect(context.Background(), &pb.ConnectRequest{ConnectionId: "c1", AdapterType: "memory", ReadOnly: readOnly})
	if err != nil || !res.GetOk() {
		t.Fatalf("connect failed: %v %v", res, err)
	}
}

func TestWriteThenReadThroughTheService(t *testing.T) {
	s := newService()
	connect(t, s, false)
	ctx := context.Background()
	created, _ := s.Create(ctx, &pb.WriteRequest{ConnectionId: "c1", Collection: "items", Values: mustStruct(t, map[string]any{"title": "First", "score": 4.0})})
	if created.GetError() != nil {
		t.Fatalf("create: %v", created.GetError().Code)
	}
	found, _ := s.Find(ctx, &pb.FindRequest{ConnectionId: "c1", Query: mustStruct(t, map[string]any{"collection": "items", "page": map[string]any{"limit": 10.0}})})
	if found.GetError() != nil || len(found.GetRecords()) != 1 {
		t.Fatalf("find: %v", found)
	}
}

func TestErrorsUseProtocolCodes(t *testing.T) {
	s := newService()
	ctx := context.Background()
	if res, _ := s.Find(ctx, &pb.FindRequest{ConnectionId: "nope", Query: mustStruct(t, map[string]any{"collection": "items"})}); res.GetError().GetCode() != "CONNECTION_FAILED" {
		t.Errorf("before connect: %v", res.GetError())
	}
	connect(t, s, true)
	res, _ := s.Create(ctx, &pb.WriteRequest{ConnectionId: "c1", Collection: "items", Values: mustStruct(t, map[string]any{"title": "x"})})
	if res.GetError().GetCode() != "READ_ONLY" {
		t.Errorf("read-only: %v", res.GetError())
	}
	bad, _ := s.Find(ctx, &pb.FindRequest{ConnectionId: "c1", Query: mustStruct(t, map[string]any{"collection": "items", "surprise": true})})
	if bad.GetError().GetCode() != "VALIDATION_FAILED" {
		t.Errorf("unknown query key: %v", bad.GetError())
	}
	wrong, _ := s.Connect(ctx, &pb.ConnectRequest{ConnectionId: "c2", AdapterType: "postgres"})
	if wrong.GetOk() || wrong.GetError().GetCode() != "VALIDATION_FAILED" {
		t.Errorf("wrong adapter type: %v", wrong)
	}
}

// brokenAdapter fails the way a real driver can: with a message full of secrets.
type brokenAdapter struct{ *adapter.Memory }

func (brokenAdapter) Find(context.Context, adapter.Query) (adapter.FindResult, error) {
	return adapter.FindResult{}, errors.New("password=hunter2 host=10.0.0.5")
}

func TestUnexpectedFailuresBecomeAGenericInternalError(t *testing.T) {
	s := New(func() adapter.Adapter { return brokenAdapter{adapter.NewMemory()} }, "memory", nil)
	connect(t, s, false)
	res, _ := s.Find(context.Background(), &pb.FindRequest{ConnectionId: "c1", Query: mustStruct(t, map[string]any{"collection": "items"})})
	e := res.GetError()
	if e.GetCode() != "INTERNAL" || strings.Contains(e.GetMessage(), "hunter2") || strings.Contains(e.GetMessage(), "10.0.0.5") {
		t.Fatalf("leaked or wrong code: %v", e)
	}
}

func TestCloseReleasesTheConnection(t *testing.T) {
	s := newService()
	connect(t, s, false)
	ctx := context.Background()
	if res, _ := s.Close(ctx, &pb.CloseRequest{ConnectionId: "c1"}); !res.GetOk() {
		t.Fatal("close failed")
	}
	res, _ := s.Find(ctx, &pb.FindRequest{ConnectionId: "c1", Query: mustStruct(t, map[string]any{"collection": "items"})})
	if res.GetError().GetCode() != "CONNECTION_FAILED" {
		t.Fatalf("after close: %v", res.GetError())
	}
}

func TestConnectionsDoNotShareData(t *testing.T) {
	s := newService()
	ctx := context.Background()
	for _, id := range []string{"c1", "c2"} {
		if res, _ := s.Connect(ctx, &pb.ConnectRequest{ConnectionId: id, AdapterType: "memory"}); !res.GetOk() {
			t.Fatal("connect")
		}
	}
	_, _ = s.Create(ctx, &pb.WriteRequest{ConnectionId: "c1", Collection: "items", Values: mustStruct(t, map[string]any{"title": "only c1"})})
	other, _ := s.Find(ctx, &pb.FindRequest{ConnectionId: "c2", Query: mustStruct(t, map[string]any{"collection": "items"})})
	if len(other.GetRecords()) != 0 {
		t.Fatal("c2 saw c1's data")
	}
}
