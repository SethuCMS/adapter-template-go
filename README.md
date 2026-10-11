# SethuCMS adapter template (Go)

Start here to write an adapter for a database in Go. You implement one interface; this template
turns it into the gRPC server the SethuCMS gateway calls (`proto/adapter.proto`).

```
adapter/adapter.go   the contract you implement (plain Go types) and the rules to follow
adapter/memory.go    a complete working adapter (in memory). Copy it and replace the storage
adapter/*_test.go    tests for it. Copy these too
server/              protocol glue: safe error handling, one adapter per connection, TLS. Usually untouched
cmd/adapter/main.go  starts the server
scripts/dev.sh       tools, proto, test and build commands
proto/adapter.proto  the protocol (copy of sethucms-docs/schemas/adapter.proto)
gen/                 generated code (created by `sh scripts/dev.sh proto`)
```

## First run

You need Go 1.22+ and `protoc` ([install](https://grpc.io/docs/protoc-installation/)).

```bash
sh scripts/dev.sh tools    # installs protoc-gen-go and protoc-gen-go-grpc
sh scripts/dev.sh proto    # generates gen/adapterv1
go mod tidy                # downloads dependencies and writes go.sum
sh scripts/dev.sh test
sh scripts/dev.sh build && ./bin/adapter    # listens on 127.0.0.1:50051
```

Commit `gen/` and `go.sum` so others can build without `protoc`.

> The `adapter` package depends on the standard library only and its tests need nothing else.
> The `server` package needs the generated code, so run `sh scripts/dev.sh proto` before `go test ./...`.

## Make your own adapter

1. Change the module name in `go.mod` (and the import paths) to your own.
2. Copy `adapter/memory.go`, rename it, and implement the methods with your database driver.
3. In `cmd/adapter/main.go` replace `adapter.NewMemory` and `"memory"` with your adapter and its type name.
4. Copy `adapter/memory_test.go` and make the same behaviours pass against a throwaway database.

Read the rules at the top of `adapter/adapter.go` before you write any query code. The short
version: **placeholders only, never paste text into a query; only use names from introspection;
be honest in `Capabilities()`; never copy driver error text into an error; honour `ReadOnly`; stop
when `ctx` is cancelled.**

## Settings

Environment variables: `ADAPTER_ADDRESS` (default `127.0.0.1:50051`).
For any non-local address you must provide TLS or the server refuses to start:
`ADAPTER_TLS_CERT`, `ADAPTER_TLS_KEY` (PEM files), and `ADAPTER_CLIENT_CA` to require the
gateway's client certificate (mutual TLS, recommended).

## Good to know

- **Numbers:** the protocol carries numbers as doubles. Whole numbers come to you as `int`;
  values above 2^53 are not exact, so send those as strings.
- **Record values** must be JSON-like (numbers, strings, bools, nil, lists, maps, `[]byte`).
  Convert times, decimals and driver types in your adapter before returning them.
- **Cursors:** return any string as `NextCursor` (an offset, a key, a token). The gateway signs
  and checks it, so a client cannot forge one. Return `""` on the last page.
- **Unknown query keys are rejected,** not ignored. A query the adapter does not fully understand never runs.
- **Unexpected errors** (anything that is not `*adapter.Error`) are reported to clients as a
  generic `INTERNAL` error. The text is not logged either, because it may hold secrets.
- `Batch` and `Subscribe` are optional in the protocol; the template reports them as unsupported.
- Every call has a 30 second deadline (`Service.timeout`).
