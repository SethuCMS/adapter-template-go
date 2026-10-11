#!/usr/bin/env sh
# Usage: sh scripts/dev.sh <command>
#   tools   install protoc-gen-go and protoc-gen-go-grpc
#   proto   generate gen/adapterv1 from proto/adapter.proto (run once, and whenever the protocol changes)
#   test    vet and run all tests
#   build   build bin/adapter
set -eu
cd "$(dirname "$0")/.."
MODULE=github.com/sethucms/sethucms-adapter-template-go

case "${1:-}" in
  tools)
    go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
    ;;
  proto)
    mkdir -p gen/adapterv1
    protoc -Iproto \
      --go_out=gen/adapterv1 --go_opt=paths=source_relative --go_opt=Madapter.proto=$MODULE/gen/adapterv1 \
      --go-grpc_out=gen/adapterv1 --go-grpc_opt=paths=source_relative --go-grpc_opt=Madapter.proto=$MODULE/gen/adapterv1 \
      proto/adapter.proto
    ;;
  test)
    go vet ./...
    go test -race -count=1 ./...
    ;;
  build)
    go build -o bin/adapter ./cmd/adapter
    ;;
  *)
    sed -n '2,6p' "$0"
    exit 1
    ;;
esac
