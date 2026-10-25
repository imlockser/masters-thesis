package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"masters-thesis/backend/internal/storage"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	store, err := storage.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer store.Close()

	return store.Migrate(ctx)
}
