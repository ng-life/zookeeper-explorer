package server

import (
	"context"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/ng-life/zookeeper-explorer/internal/config"
)

// Run serves HTTP until the context is canceled, then closes cluster sessions.
func Run(ctx context.Context, c config.Config) error {
	a := New(c)
	defer a.Close()
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	log.Printf("ZooKeeper Explorer is ready at %s", listener.Addr())
	select {
	case err := <-done:
		if err != http.ErrServerClosed {
			return err
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			server.Close()
			return err
		}
	}
	return nil
}
