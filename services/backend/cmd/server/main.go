package main

import (
	"cmp"
	"context"
	"encoding/hex"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"masters-thesis/backend/internal/agent"
	"masters-thesis/backend/internal/auth"
	"masters-thesis/backend/internal/provisioning"
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

	workerKey, err := hex.DecodeString(os.Getenv("WORKER_CALLBACK_KEY"))
	if err != nil || len(workerKey) != 32 {
		return errors.New("WORKER_CALLBACK_KEY must contain 64 hexadecimal characters")
	}
	authenticator, err := auth.New(auth.Config{
		Issuer:       os.Getenv("ZITADEL_ISSUER"),
		ProjectID:    os.Getenv("ZITADEL_PROJECT_ID"),
		ClientID:     os.Getenv("ZITADEL_CLIENT_ID"),
		ClientSecret: os.Getenv("ZITADEL_CLIENT_SECRET"),
	})
	if err != nil {
		return err
	}

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
	service := agent.NewService(ctx, store, queue, workerKey)
	defer service.Close()
	bridge := rpc.NewHandler(store.SaveUserMemory, rpc.AgentMethods(service), rpc.ChatMethods(store))

	mux := http.NewServeMux()
	mux.Handle("POST /internal/zitadel/events", provisioning.New(provisioning.Config{
		Issuer:         os.Getenv("ZITADEL_ISSUER"),
		ProjectID:      os.Getenv("ZITADEL_PROJECT_ID"),
		OrganizationID: os.Getenv("ZITADEL_ALLOWED_ORG_ID"),
		InstanceID:     os.Getenv("ZITADEL_INSTANCE_ID"),
		SigningKey:     os.Getenv("ZITADEL_ACTIONS_SIGNING_KEY"),
		PAT:            os.Getenv("ZITADEL_PROVISIONER_PAT"),
	}))
	mux.Handle("POST /internal/agent/memory", http.MaxBytesHandler(http.HandlerFunc(service.SaveRunMemory), 1<<20))
	mux.Handle("POST /rpc", authenticator.Middleware(auth.RequireUserRole(http.MaxBytesHandler(bridge, 1<<20))))
	mux.Handle("GET /agent/runs/{run_id}/events", authenticator.Middleware(auth.RequireUserRole(http.HandlerFunc(service.Stream))))
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
