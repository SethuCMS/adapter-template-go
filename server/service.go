// Package server turns your adapter.Adapter into the gRPC service the gateway calls.
//
// You normally do not edit this package. It does the safety work that is the same for every adapter:
//
//   - one adapter instance per connection id;
//   - known failures (*adapter.Error) become the protocol's Error; anything else becomes a generic
//     INTERNAL error, so a driver's message can never reach a client;
//   - every call has a deadline;
//   - nothing sensitive (credentials, config, records) is ever logged.
package server

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/sethucms/sethucms-adapter-template-go/adapter"
	pb "github.com/sethucms/sethucms-adapter-template-go/gen/adapterv1"
)

// Service implements the gRPC Adapter service.
type Service struct {
	pb.UnimplementedAdapterServer // Subscribe (realtime) is optional and not supported by this template

	factory     func() adapter.Adapter
	adapterType string
	timeout     time.Duration
	log         *slog.Logger

	mu       sync.Mutex
	adapters map[string]adapter.Adapter
}

// New creates the service. factory is called once per connection; adapterType is the name the
// gateway uses for this kind of database (for example "postgres").
func New(factory func() adapter.Adapter, adapterType string, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		factory: factory, adapterType: adapterType, log: log,
		timeout: 30 * time.Second, adapters: map[string]adapter.Adapter{},
	}
}

func (s *Service) adapterFor(connectionID string) (adapter.Adapter, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.adapters[connectionID]
	if !ok {
		return nil, adapter.NewError(adapter.ConnectionFailed, "The connection is not open")
	}
	return a, nil
}

// protoError converts any error into the protocol's Error without leaking its text.
func (s *Service) protoError(label string, err error) *pb.Error {
	var known *adapter.Error
	if errors.As(err, &known) {
		out := &pb.Error{Code: string(known.Code), Message: known.Message}
		if len(known.Details) > 0 {
			if details, convErr := mapToStruct(known.Details); convErr == nil {
				out.Details = details
			}
		}
		return out
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &pb.Error{Code: string(adapter.Timeout), Message: "The request took too long"}
	}
	s.log.Error("unexpected adapter failure", "rpc", label) // the error text stays out of the logs too: it may hold secrets
	return &pb.Error{Code: string(adapter.Internal), Message: "The adapter failed unexpectedly"}
}

func (s *Service) withDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, s.timeout)
}

func (s *Service) Connect(ctx context.Context, req *pb.ConnectRequest) (*pb.ConnectResponse, error) {
	ctx, cancel := s.withDeadline(ctx)
	defer cancel()
	if req.GetAdapterType() != "" && req.GetAdapterType() != s.adapterType {
		return &pb.ConnectResponse{Error: s.protoError("Connect", adapter.NewError(adapter.ValidationFailed, "This server does not handle that adapter type"))}, nil
	}
	a := s.factory()
	err := a.Connect(ctx, adapter.ConnectInput{
		Config:      structToMap(req.GetConfig()),
		Credentials: structToMap(req.GetCredentials()),
		ReadOnly:    req.GetReadOnly(),
	})
	if err != nil {
		_ = a.Close()
		return &pb.ConnectResponse{Error: s.protoError("Connect", err)}, nil
	}
	s.mu.Lock()
	previous := s.adapters[req.GetConnectionId()]
	s.adapters[req.GetConnectionId()] = a
	s.mu.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
	return &pb.ConnectResponse{Ok: true}, nil
}

func (s *Service) Introspect(ctx context.Context, req *pb.IntrospectRequest) (*pb.IntrospectResponse, error) {
	ctx, cancel := s.withDeadline(ctx)
	defer cancel()
	a, err := s.adapterFor(req.GetConnectionId())
	if err != nil {
		return &pb.IntrospectResponse{Error: s.protoError("Introspect", err)}, nil
	}
	collections, err := a.Introspect(ctx)
	if err != nil {
		return &pb.IntrospectResponse{Error: s.protoError("Introspect", err)}, nil
	}
	out := &pb.IntrospectResponse{}
	for _, c := range collections {
		info := &pb.CollectionInfo{NativeName: c.NativeName}
		for _, f := range c.Fields {
			info.Fields = append(info.Fields, &pb.FieldInfo{
				NativeName: f.NativeName, NativeType: f.NativeType, CanonicalType: f.CanonicalType,
				Required: f.Required, IsPrimaryKey: f.IsPrimaryKey, Lossy: f.Lossy,
			})
		}
		for _, r := range c.Relations {
			mapping, _ := mapToStruct(r.Mapping)
			info.Relations = append(info.Relations, &pb.RelationInfo{Name: r.Name, Target: r.Target, Kind: r.Kind, Mapping: mapping})
		}
		out.Collections = append(out.Collections, info)
	}
	return out, nil
}

func (s *Service) Capabilities(_ context.Context, req *pb.CapabilitiesRequest) (*pb.CapabilitiesResponse, error) {
	a, err := s.adapterFor(req.GetConnectionId())
	if err != nil {
		return &pb.CapabilitiesResponse{}, nil // the most restrictive answer: nothing is supported
	}
	c := a.Capabilities()
	return &pb.CapabilitiesResponse{
		Joins: c.Joins, Transactions: c.Transactions, FilterOr: c.FilterOr,
		FilterInequality: c.FilterInequality, FullText: c.FullText, Sort: c.Sort,
		OffsetPagination: c.OffsetPagination, Aggregations: c.Aggregations,
		Realtime: c.Realtime, Schema: c.Schema, Writes: c.Writes,
	}, nil
}

func (s *Service) Find(ctx context.Context, req *pb.FindRequest) (*pb.FindResponse, error) {
	ctx, cancel := s.withDeadline(ctx)
	defer cancel()
	a, err := s.adapterFor(req.GetConnectionId())
	if err != nil {
		return &pb.FindResponse{Error: s.protoError("Find", err)}, nil
	}
	query, err := parseQuery(structToMap(req.GetQuery()))
	if err != nil {
		return &pb.FindResponse{Error: s.protoError("Find", err)}, nil
	}
	result, err := a.Find(ctx, query)
	if err != nil {
		return &pb.FindResponse{Error: s.protoError("Find", err)}, nil
	}
	out := &pb.FindResponse{NextCursor: result.NextCursor, CapabilityNotes: result.Notes}
	for _, record := range result.Records {
		st, convErr := mapToStruct(record)
		if convErr != nil {
			return &pb.FindResponse{Error: s.protoError("Find", convErr)}, nil
		}
		out.Records = append(out.Records, st)
	}
	return out, nil
}

func (s *Service) Get(ctx context.Context, req *pb.GetRequest) (*pb.GetResponse, error) {
	ctx, cancel := s.withDeadline(ctx)
	defer cancel()
	a, err := s.adapterFor(req.GetConnectionId())
	if err != nil {
		return &pb.GetResponse{Error: s.protoError("Get", err)}, nil
	}
	record, err := a.Get(ctx, req.GetCollection(), req.GetId())
	if err != nil {
		return &pb.GetResponse{Error: s.protoError("Get", err)}, nil
	}
	st, err := mapToStruct(record)
	if err != nil {
		return &pb.GetResponse{Error: s.protoError("Get", err)}, nil
	}
	return &pb.GetResponse{Record: st}, nil
}

func (s *Service) Create(ctx context.Context, req *pb.WriteRequest) (*pb.WriteResponse, error) {
	return s.write(ctx, "Create", req, func(ctx context.Context, a adapter.Adapter) (adapter.Record, error) {
		return a.Create(ctx, req.GetCollection(), structToMap(req.GetValues()))
	})
}

func (s *Service) Update(ctx context.Context, req *pb.WriteRequest) (*pb.WriteResponse, error) {
	return s.write(ctx, "Update", req, func(ctx context.Context, a adapter.Adapter) (adapter.Record, error) {
		return a.Update(ctx, req.GetCollection(), req.GetId(), structToMap(req.GetValues()))
	})
}

func (s *Service) write(ctx context.Context, label string, req *pb.WriteRequest, run func(context.Context, adapter.Adapter) (adapter.Record, error)) (*pb.WriteResponse, error) {
	ctx, cancel := s.withDeadline(ctx)
	defer cancel()
	a, err := s.adapterFor(req.GetConnectionId())
	if err != nil {
		return &pb.WriteResponse{Error: s.protoError(label, err)}, nil
	}
	record, err := run(ctx, a)
	if err != nil {
		return &pb.WriteResponse{Error: s.protoError(label, err)}, nil
	}
	st, err := mapToStruct(record)
	if err != nil {
		return &pb.WriteResponse{Error: s.protoError(label, err)}, nil
	}
	return &pb.WriteResponse{Record: st}, nil
}

func (s *Service) Delete(ctx context.Context, req *pb.DeleteRequest) (*pb.DeleteResponse, error) {
	ctx, cancel := s.withDeadline(ctx)
	defer cancel()
	a, err := s.adapterFor(req.GetConnectionId())
	if err != nil {
		return &pb.DeleteResponse{Error: s.protoError("Delete", err)}, nil
	}
	if err := a.Delete(ctx, req.GetCollection(), req.GetId()); err != nil {
		return &pb.DeleteResponse{Error: s.protoError("Delete", err)}, nil
	}
	return &pb.DeleteResponse{Ok: true}, nil
}

// Batch is optional in the protocol. Implement it only if your database can do it safely.
func (s *Service) Batch(_ context.Context, _ *pb.BatchRequest) (*pb.BatchResponse, error) {
	return &pb.BatchResponse{Error: s.protoError("Batch", adapter.NewError(adapter.CapabilityUnsupported, "Batch writes are not supported"))}, nil
}

func (s *Service) Close(_ context.Context, req *pb.CloseRequest) (*pb.CloseResponse, error) {
	s.mu.Lock()
	a := s.adapters[req.GetConnectionId()]
	delete(s.adapters, req.GetConnectionId())
	s.mu.Unlock()
	if a != nil {
		_ = a.Close()
	}
	return &pb.CloseResponse{Ok: true}, nil
}

// CloseAll releases every connection. Call it when the server stops.
func (s *Service) CloseAll() {
	s.mu.Lock()
	all := s.adapters
	s.adapters = map[string]adapter.Adapter{}
	s.mu.Unlock()
	for _, a := range all {
		_ = a.Close()
	}
}
