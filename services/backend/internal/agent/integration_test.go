package agent_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"masters-thesis/backend/internal/agent"
	"masters-thesis/backend/internal/rpc"
	"masters-thesis/backend/internal/storage"
)

func isolatedDatabase(t *testing.T, base string) string {
	t.Helper()
	admin, err := pgx.Connect(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close(context.Background()) })
	name := "backend_agent_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(context.Background(), `CREATE DATABASE "`+name+`"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), `DROP DATABASE "`+name+`" WITH (FORCE)`); err != nil {
			t.Error(err)
		}
	})
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + name
	return parsed.String()
}

func startWorker(t *testing.T, databaseURL, backendURL string) *exec.Cmd {
	t.Helper()
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("uv", "run", "python", filepath.Join(root, "tests/fixtures/agent_worker.py"))
	command.Dir = root
	command.Env = append(os.Environ(), "PYTHONPATH="+root, "TEST_DBOS_DATABASE_URL="+databaseURL, "TEST_BACKEND_RPC_URL="+backendURL+"/rpc")
	logFile, err := os.CreateTemp(t.TempDir(), "worker-*.log")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logFile.Close() })
	command.Stderr = logFile
	t.Cleanup(func() {
		if t.Failed() {
			logs, _ := os.ReadFile(logFile.Name())
			t.Logf("Python worker log: %s", logs)
		}
	})
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- command.Wait() }()
	t.Cleanup(func() {
		command.Process.Signal(syscall.SIGTERM)
		select {
		case <-finished:
		case <-time.After(10 * time.Second):
			command.Process.Kill()
			<-finished
		}
	})
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if scanner.Text() == "READY" {
				ready <- true
				io.Copy(io.Discard, stdout)
				return
			}
		}
		ready <- false
	}()
	select {
	case ok := <-ready:
		if !ok {
			logs, _ := os.ReadFile(logFile.Name())
			t.Fatalf("Python worker did not start: %s", logs)
		}
	case <-time.After(45 * time.Second):
		t.Fatal("Python worker startup timed out")
	}
	return command
}

func callMethod(t *testing.T, serverURL, method string, params map[string]any) (json.RawMessage, int) {
	t.Helper()
	encoded, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "request", "method": method, "params": params})
	response, err := http.Post(serverURL+"/rpc", "application/json", strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var payload struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error != nil {
		return payload.Result, payload.Error.Code
	}
	return payload.Result, 0
}

func rpcCall(t *testing.T, serverURL string, params map[string]any) (agent.StartedRun, int) {
	t.Helper()
	result, code := callMethod(t, serverURL, "SendMessage", params)
	var started agent.StartedRun
	if code == 0 {
		if err := json.Unmarshal(result, &started); err != nil {
			t.Fatal(err)
		}
	}
	return started, code
}

func waitRun(t *testing.T, store *storage.Store, runID, userID string) storage.AgentRun {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		run, err := store.GetRun(context.Background(), runID, userID)
		if err != nil {
			t.Fatal(err)
		}
		if run.State != "pending" {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Run did not finish")
	return storage.AgentRun{}
}

func TestPythonAgentQueueAndHTTPStream(t *testing.T) {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL is required for isolated Go/Python integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	store, err := storage.Open(ctx, isolatedDatabase(t, base))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	runtimeURL := isolatedDatabase(t, base)
	worker := startWorker(t, runtimeURL, server.URL)
	queue, err := agent.NewDBOSQueue(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { queue.Close() })
	service := agent.NewService(ctx, store, queue)
	t.Cleanup(service.Close)
	bridge := rpc.NewHandler(store.SaveUserMemory, rpc.AgentMethods(service), rpc.ChatMethods(store))
	t.Cleanup(func() { bridge.Close() })
	mux.Handle("POST /rpc", bridge)
	mux.HandleFunc("GET /agent/runs/{run_id}/events", service.Stream)

	chat, err := store.CreateChat(ctx, "user-1", "Forecast")
	if err != nil {
		t.Fatal(err)
	}
	add := func(role, content string) {
		encoded, _ := json.Marshal(content)
		if _, err := store.AddMessage(ctx, chat.ID, chat.UserID, role, storage.MessageContent{Content: encoded}); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 6 {
		add("user", fmt.Sprintf("Question %d", i))
		add("assistant", fmt.Sprintf("Answer %d", i))
	}
	if _, err := store.SaveUserMemory(ctx, chat.UserID, "Prefers solar forecasts.", 0); err != nil {
		t.Fatal(err)
	}
	requestID := uuid.NewString()
	params := map[string]any{"request_id": requestID, "chat_id": chat.ID, "user_id": chat.UserID, "content": "forecast"}
	started, code := rpcCall(t, server.URL, params)
	if code != 0 || started.RunID != requestID {
		t.Fatalf("SendMessage failed: %+v, code=%d", started, code)
	}
	run, err := store.GetRun(ctx, started.RunID, chat.UserID)
	if err != nil || len(run.Payload.Messages) != 11 || string(run.Payload.Messages[0].Content) != `"Question 1"` || run.Payload.MemoryVersion != 1 {
		t.Fatalf("Incorrect five-turn snapshot: %+v, %v", run, err)
	}
	duplicate, code := rpcCall(t, server.URL, params)
	if code != 0 || duplicate.RunID != started.RunID {
		t.Fatalf("Duplicate request was not idempotent: %+v, %d", duplicate, code)
	}
	params["content"] = "different"
	if _, code := rpcCall(t, server.URL, params); code != int(rpc.RequestReuse) {
		t.Fatalf("Expected request reuse error, got %d", code)
	}
	params["request_id"] = uuid.NewString()
	if _, code := rpcCall(t, server.URL, params); code != int(rpc.ChatBusy) {
		t.Fatalf("Expected busy chat, got %d", code)
	}
	if _, err := store.AddMessage(ctx, chat.ID, chat.UserID, "user", storage.MessageContent{Content: json.RawMessage(`"bypass"`)}); !errors.Is(err, storage.ErrChatBusy) {
		t.Fatalf("Active run could be bypassed: %v", err)
	}

	client := &http.Client{Timeout: 20 * time.Second}
	response, err := client.Get(server.URL + started.EventsURL)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("SSE did not open immediately: %d", response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	lastID := -1
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "id: ") {
			lastID, _ = strconv.Atoi(strings.TrimPrefix(line, "id: "))
		}
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, `"type":"text_delta"`) {
			break
		}
	}
	response.Body.Close()
	if lastID < 0 || scanner.Err() != nil {
		t.Fatalf("No live delta before completion: %d, %v", lastID, scanner.Err())
	}
	if run, _ := store.GetRun(ctx, started.RunID, chat.UserID); run.State != "pending" {
		t.Fatalf("Agent finished before live delta: %s", run.State)
	}
	if err := worker.Process.Signal(syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	run = waitRun(t, store, started.RunID, chat.UserID)
	var artifacts []struct {
		Data struct {
			Total float64 `json:"total_energy_mwh"`
		} `json:"data"`
	}
	if json.Unmarshal(run.Artifacts, &artifacts) != nil || run.State != "completed" || len(artifacts) != 1 || artifacts[0].Data.Total != 42 {
		t.Fatalf("Disconnected SSE prevented persistence: %+v", run)
	}
	history, err := store.GetHistory(ctx, chat.ID, chat.UserID, 1)
	if err != nil || len(history) != 4 || history[1].Role != "assistant" || history[2].Role != "tool" || len(history[1].ToolCalls) != 1 {
		t.Fatalf("Tool turn was not persisted in LLM order: %+v, %v", history, err)
	}
	if strings.Contains(string(history[2].Content), "total_energy_mwh") {
		t.Fatal("Structured artifact leaked into LLM history")
	}

	request, _ := http.NewRequest(http.MethodGet, server.URL+started.EventsURL, nil)
	request.Header.Set("Last-Event-ID", strconv.Itoa(lastID))
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || !strings.Contains(string(body), "event: run_completed") || strings.Contains(string(body), "event: run_started") {
		t.Fatalf("SSE resume failed: %s, %v", body, err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "id: ") {
			id, _ := strconv.Atoi(strings.TrimPrefix(line, "id: "))
			if id <= lastID {
				t.Fatalf("SSE replay duplicated event %d", id)
			}
		}
	}
	if after, _ := store.GetHistory(ctx, chat.ID, chat.UserID, 1); len(after) != len(history) {
		t.Fatal("SSE replay duplicated stored messages")
	}

	for _, test := range []struct {
		path   string
		lastID string
		status int
	}{
		{"/agent/runs/" + started.RunID + "/events?user_id=other", "", http.StatusNotFound},
		{started.EventsURL, "invalid", http.StatusBadRequest},
		{started.EventsURL, "-1", http.StatusBadRequest},
		{"/agent/runs/not-a-uuid/events?user_id=user-1", "", http.StatusBadRequest},
	} {
		request, _ := http.NewRequest(http.MethodGet, server.URL+test.path, nil)
		request.Header.Set("Last-Event-ID", test.lastID)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != test.status {
			t.Fatalf("Expected HTTP %d, got %d", test.status, response.StatusCode)
		}
	}
	for _, test := range []struct {
		params map[string]any
		code   int
	}{
		{map[string]any{"request_id": uuid.NewString(), "chat_id": chat.ID, "user_id": "other", "content": "forecast"}, int(rpc.ChatNotFound)},
		{map[string]any{"request_id": "invalid", "chat_id": chat.ID, "user_id": chat.UserID, "content": "forecast"}, -32602},
		{map[string]any{"request_id": uuid.NewString(), "chat_id": chat.ID, "user_id": chat.UserID, "content": "forecast", "history_turns": 10}, -32602},
	} {
		if _, code := rpcCall(t, server.URL, test.params); code != test.code {
			t.Fatalf("Expected RPC code %d, got %d", test.code, code)
		}
	}

	for _, content := range []string{"remember", "fail", "forecast"} {
		params = map[string]any{"request_id": uuid.NewString(), "chat_id": chat.ID, "user_id": chat.UserID, "content": content}
		started, code := rpcCall(t, server.URL, params)
		if code != 0 {
			t.Fatalf("Could not start %s: %d", content, code)
		}
		run := waitRun(t, store, started.RunID, chat.UserID)
		expected := "completed"
		if content == "fail" {
			expected = "failed"
		}
		if run.State != expected {
			t.Fatalf("Expected %s for %s, got %s", expected, content, run.State)
		}
	}
	memory, err := store.GetUserMemory(ctx, chat.UserID)
	if err != nil || memory.Version != 2 || memory.Summary != "Prefers solar forecasts and UTC times." {
		t.Fatalf("remember did not reach backend: %+v, %v", memory, err)
	}

	chat, err = store.CreateChat(ctx, "user-1", "Resume prepared run")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := store.PrepareRun(ctx, uuid.NewString(), chat.ID, chat.UserID, "forecast")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if run := waitRun(t, store, prepared.ID, chat.UserID); run.State != "completed" {
		t.Fatalf("Prepared run did not resume: %s", run.State)
	}

	if err := worker.Process.Signal(syscall.SIGUSR2); err != nil {
		t.Fatal(err)
	}
	var requests sync.WaitGroup
	results := make(chan agent.StartedRun, 10)
	for i := range 10 {
		chat, err := store.CreateChat(ctx, fmt.Sprintf("parallel-%d", i), "Parallel forecast")
		if err != nil {
			t.Fatal(err)
		}
		requests.Go(func() {
			started, err := service.SendMessage(ctx, uuid.NewString(), chat.ID, chat.UserID, "forecast")
			if err != nil {
				t.Error(err)
			}
			results <- started
		})
	}
	requests.Wait()
	close(results)
	var pending []storage.AgentRun
	for started := range results {
		response, err := client.Get(server.URL + started.EventsURL)
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(response.Body)
		live := false
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), `"type":"text_delta"`) {
				live = true
				break
			}
		}
		response.Body.Close()
		query, _ := url.Parse(started.EventsURL)
		run, err := store.GetRun(ctx, started.RunID, query.Query().Get("user_id"))
		if err != nil || run.State != "pending" || !live {
			t.Fatalf("Parallel run did not stream while pending: %+v, %v", run, err)
		}
		pending = append(pending, run)
	}
	queuedChat, err := store.CreateChat(ctx, "queued-user", "Queued forecast")
	if err != nil {
		t.Fatal(err)
	}
	queued, err := service.SendMessage(ctx, uuid.NewString(), queuedChat.ID, queuedChat.UserID, "forecast")
	if err != nil {
		t.Fatal(err)
	}
	response, err = client.Get(server.URL + queued.EventsURL)
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	response.Body.Close()
	if err != nil || line != ": connected\n" {
		t.Fatalf("SSE did not open while all ten workers were occupied: %q, %v", line, err)
	}
	if err := worker.Process.Signal(syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	for _, run := range pending {
		if result := waitRun(t, store, run.ID, run.Payload.UserID); result.State != "completed" {
			t.Fatalf("Parallel run failed: %s", result.State)
		}
	}
	if run := waitRun(t, store, queued.RunID, queuedChat.UserID); run.State != "completed" {
		t.Fatalf("Queued run did not complete: %s", run.State)
	}
	t.Log("Verified Go → Python DBOS, live SSE, replay, five-turn history, artifacts, memory RPC, failure, and disconnect persistence")
}
