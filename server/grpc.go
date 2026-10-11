package server

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	pb "github.com/sethucms/sethucms-adapter-template-go/gen/adapterv1"
)

const maxMessageBytes = 8 * 1024 * 1024

// NewGRPCServer registers the service on a new gRPC server.
// Pass nil creds only for localhost; use LoadTLS for anything else.
func NewGRPCServer(svc *Service, creds credentials.TransportCredentials) *grpc.Server {
	options := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(maxMessageBytes),
		grpc.MaxSendMsgSize(maxMessageBytes),
	}
	if creds != nil {
		options = append(options, grpc.Creds(creds))
	}
	server := grpc.NewServer(options...)
	pb.RegisterAdapterServer(server, svc)
	return server
}

// Listen opens the address. Without TLS it refuses anything that is not local, so a database
// adapter is never exposed in plain text by accident.
func Listen(address string, withTLS bool) (net.Listener, error) {
	if !withTLS {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("invalid address %q", address)
		}
		if host != "127.0.0.1" && host != "localhost" && host != "::1" && !strings.HasPrefix(address, "unix:") {
			return nil, errors.New("refusing to serve plain-text gRPC on a non-local address: provide TLS certificates")
		}
	}
	return net.Listen("tcp", address)
}

// LoadTLS builds server credentials. If clientCAFile is set, clients must present a certificate
// signed by that CA (mutual TLS), which is how the gateway should authenticate to the adapter.
func LoadTLS(certFile, keyFile, clientCAFile string) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, errors.New("could not load the TLS certificate and key")
	}
	config := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}
	if clientCAFile != "" {
		pem, err := os.ReadFile(clientCAFile)
		if err != nil {
			return nil, errors.New("could not read the client CA file")
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("the client CA file has no valid certificates")
		}
		config.ClientCAs = pool
		config.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return credentials.NewTLS(config), nil
}
