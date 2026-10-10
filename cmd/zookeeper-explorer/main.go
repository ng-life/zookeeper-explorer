package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ng-life/zookeeper-explorer/internal/config"
	"github.com/ng-life/zookeeper-explorer/internal/server"
)

func main() {
	c, err := config.Load(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Run(ctx, c); err != nil {
		log.Fatal(err)
	}
}
