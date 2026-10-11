// Command adapter runs the adapter as a gRPC server.
//
// Settings (environment variables):
//
//	ADAPTER_ADDRESS    default 127.0.0.1:50051
//	ADAPTER_TLS_CERT, ADAPTER_TLS_KEY   PEM files; required for any non-local address
//	ADAPTER_CLIENT_CA  PEM file; when set, the gateway must present a certificate signed by it
package main

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc/credentials"

	"github.com/sethucms/sethucms-adapter-template-go/adapter"
	"github.com/sethucms/sethucms-adapter-template-go/server"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("stopped", "error", err.Error())
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	address := os.Getenv("ADAPTER_ADDRESS")
	if address == "" {
		address = "127.0.0.1:50051"
	}

	var creds credentials.TransportCredentials
	certFile, keyFile := os.Getenv("ADAPTER_TLS_CERT"), os.Getenv("ADAPTER_TLS_KEY")
	if certFile != "" && keyFile != "" {
		loaded, err := server.LoadTLS(certFile, keyFile, os.Getenv("ADAPTER_CLIENT_CA"))
		if err != nil {
			return err
		}
		creds = loaded
	}

	listener, err := server.Listen(address, creds != nil)
	if err != nil {
		return err
	}

	// Replace adapter.NewMemory and "memory" with your adapter and its type name.
	service := server.New(func() adapter.Adapter { return adapter.NewMemory() }, "memory", log)
	grpcServer := server.NewGRPCServer(service, creds)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		grpcServer.GracefulStop()
	}()

	log.Info("listening", "address", listener.Addr().String(), "tls", creds != nil)
	err = grpcServer.Serve(listener)
	service.CloseAll()
	return err
}
