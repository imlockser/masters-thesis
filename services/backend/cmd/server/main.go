package main

import (
	"cmp"
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"masters-thesis/backend/internal/agent"
	"masters-thesis/backend/internal/rpc"
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

	queue, err := agent.NewDBOSQueue(ctx, os.Getenv("DBOS_SYSTEM_DATABASE_URL"))
	if err != nil {
		return err
	}
	defer queue.Close()
	service := agent.NewService(ctx, store, queue)
	defer service.Close()
	bridge := rpc.NewHandler(store.SaveUserMemory, rpc.AgentMethods(service), rpc.ChatMethods(store))
	defer bridge.Close()

	mux := http.NewServeMux()
	mux.Handle("POST /rpc", http.MaxBytesHandler(bridge, 1<<20))
	mux.HandleFunc("GET /agent/runs/{run_id}/events", service.Stream)
	server := &http.Server{
		Addr:              net.JoinHostPort("127.0.0.1", cmp.Or(os.Getenv("PORT"), "8081")),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
	}

	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	defer server.Close()
	stopped := make(chan error, 1)
	go func() { stopped <- server.Serve(listener) }()
	log.Printf("Backend listening on http://%s", server.Addr)
	if err := service.Resume(ctx); err != nil {
		return err
	}

	select {
	case err := <-stopped:
		return err
	case <-ctx.Done():
		shutdown, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if err := server.Shutdown(shutdown); err != nil {
			server.Close()
			return err
		}
		if err := <-stopped; !errors.Is(err, http.ErrServerClosed) {
			return err
		}

		return nil
	}
}
