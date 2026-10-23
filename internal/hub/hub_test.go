package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func startHub(t *testing.T) (*Hub, *Client, string) {
	t.Helper()
	h := New("test", 0)
	server := httptest.NewServer(h.Handler())
	t.Cleanup(server.Close)
	address, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(address.Port())
	return h, NewClient(port), server.URL
}

// fakeExtension behaves like tiled-mcp.js: says hello, then answers calls by
// echoing the tool name and arguments.
func fakeExtension(t *testing.T, base string, ctx context.Context) {
	t.Helper()
	post(t, base+"/hello", Hello{Version: "test", Tiled: "1.12.2", Tools: []Tool{{Name: "echo", InputSchema: json.RawMessage(`{"type":"object"}`)}}})
	go func() {
		for ctx.Err() == nil {
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/next", nil)
			request.Header.Set(Header, "1")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				return
			}
			if response.StatusCode == http.StatusNoContent {
				response.Body.Close()
				continue
			}
			var call Call
			_ = json.NewDecoder(response.Body).Decode(&call)
			response.Body.Close()
			echo, _ := json.Marshal(map[string]any{"name": call.Name, "arguments": call.Arguments})
			post(t, base+"/result", result{ID: call.ID, Result: echo})
		}
	}()
}

func post(t *testing.T, address string, body any) {
	t.Helper()
	data, _ := json.Marshal(body)
	request, _ := http.NewRequest(http.MethodPost, address, bytes.NewReader(data))
	request.Header.Set(Header, "1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Errorf("post %s: %v", address, err)
		return
	}
	response.Body.Close()
}

func TestCallReachesExtensionAndReturnsResult(t *testing.T) {
	_, client, base := startHub(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fakeExtension(t, base, ctx)

	out, err := client.Call(ctx, "echo", json.RawMessage(`{"x":1}`))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "echo" || string(got.Arguments) != `{"x":1}` {
		t.Fatalf("got %s %s", got.Name, got.Arguments)
	}
}

func TestCallWithoutExtensionFailsAtOnce(t *testing.T) {
	_, client, _ := startHub(t)
	_, err := client.Call(context.Background(), "echo", nil)
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("got %v, want ErrNotConnected", err)
	}
}

func TestToolsWaitForHello(t *testing.T) {
	_, client, base := startHub(t)
	first, err := client.Tools(context.Background(), -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Tools) != 0 {
		t.Fatalf("tools before hello: %v", first.Tools)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(50 * time.Millisecond)
		fakeExtension(t, base, ctx)
	}()
	next, err := client.Tools(ctx, first.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Tools) != 1 || next.Tools[0].Name != "echo" {
		t.Fatalf("tools after hello: %v", next.Tools)
	}
}

func TestRequestsWithoutHeaderOrFromForeignHostAreRejected(t *testing.T) {
	_, _, base := startHub(t)

	response, err := http.Get(base + "/status")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("no header: %d", response.StatusCode)
	}

	request, _ := http.NewRequest(http.MethodGet, base+"/status", nil)
	request.Header.Set(Header, "1")
	request.Host = "evil.example:80"
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign host: %d", response.StatusCode)
	}
}

func TestNextAfterRestartAsksForHello(t *testing.T) {
	_, _, base := startHub(t)
	request, _ := http.NewRequest(http.MethodGet, base+"/next", nil)
	request.Header.Set(Header, "1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("got %d, want 409", response.StatusCode)
	}
}

func TestCallNotTakenInTimeIsDropped(t *testing.T) {
	saved := PickupTimeout
	PickupTimeout = 50 * time.Millisecond
	defer func() { PickupTimeout = saved }()

	h, client, base := startHub(t)
	// Hello without polling: the hub counts the extension as connected for a
	// while, but nobody takes the call.
	post(t, base+"/hello", Hello{Version: "test"})
	_, err := client.Call(context.Background(), "echo", nil)
	if err == nil {
		t.Fatal("call succeeded without an extension polling")
	}
	if len(h.queue) != 1 {
		t.Fatalf("queue: %d", len(h.queue))
	}
	// A late poll must skip the dropped call and time out instead.
	savedPoll := PollTimeout
	PollTimeout = 50 * time.Millisecond
	defer func() { PollTimeout = savedPoll }()
	request, _ := http.NewRequest(http.MethodGet, base+"/next", nil)
	request.Header.Set(Header, "1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("got %d, want 204", response.StatusCode)
	}
}
