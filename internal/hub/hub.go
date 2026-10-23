// Package hub is the bridge between agent sessions and the Tiled extension.
//
// The Tiled scripting engine can make outgoing HTTP requests but cannot listen
// on a port or schedule timers, so the extension keeps a long-poll request open
// to the hub and executes whatever command comes back. Agent sessions post
// calls to the hub and wait for the extension's result.
package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Header must be present on every request. Browsers cannot add a custom header
// to a cross-origin request without a CORS preflight, which the hub never
// answers, so web pages cannot talk to the hub.
const Header = "X-Tiled-MCP"

// DefaultPort is the loopback port the hub listens on.
const DefaultPort = 47317

// Timings of the long-poll protocol. Variables so tests can shorten them.
var (
	// PollTimeout is how long /next and /tools hold a request open.
	PollTimeout = 25 * time.Second
	// PickupTimeout is how long a call waits for the extension to take it.
	PickupTimeout = 10 * time.Second
	// ResultTimeout is how long a call waits for the extension's result.
	ResultTimeout = 60 * time.Second
	// IdleTimeout stops the hub when neither the extension nor agents use it.
	IdleTimeout = 2 * time.Minute
	// ConnectedWindow is how recently the extension must have polled to count
	// as connected.
	ConnectedWindow = PollTimeout + 5*time.Second
)

// Tool is a tool declared by the extension, in MCP form.
type Tool struct {
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Annotations json.RawMessage `json:"annotations,omitempty"`
}

// Hello is what the extension sends when it connects.
type Hello struct {
	Version string `json:"version"`
	Tiled   string `json:"tiled"`
	Project string `json:"project,omitempty"`
	Tools   []Tool `json:"tools"`
}

// Call is a tool call sent to the extension.
type Call struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// Status describes the hub and the connected extension.
type Status struct {
	Version   string `json:"version"`
	Connected bool   `json:"connected"`
	Extension *Hello `json:"extension,omitempty"`
	Revision  int    `json:"revision"`
}

// ToolList is the answer of /tools.
type ToolList struct {
	Revision int    `json:"revision"`
	Tools    []Tool `json:"tools"`
}

type result struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
}

type pending struct {
	call   Call
	picked chan struct{}
	done   chan json.RawMessage
}

// Hub holds the extension's tools and routes calls to it.
type Hub struct {
	version string
	port    int

	mu        sync.Mutex
	hello     *Hello
	revision  int
	changed   chan struct{} // closed and replaced when tools change
	lastPoll  time.Time
	polling   int
	lastUse   time.Time
	nextID    int
	queue     chan *pending
	inFlight  map[string]*pending
	shutdown  chan struct{}
	closeOnce sync.Once
}

// New returns a hub reporting the given version.
func New(version string, port int) *Hub {
	return &Hub{
		version:  version,
		port:     port,
		changed:  make(chan struct{}),
		lastUse:  time.Now(),
		queue:    make(chan *pending, 64),
		inFlight: map[string]*pending{},
		shutdown: make(chan struct{}),
	}
}

// Handler serves the hub protocol.
func (h *Hub) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", h.handleStatus)
	mux.HandleFunc("POST /hello", h.handleHello)
	mux.HandleFunc("GET /next", h.handleNext)
	mux.HandleFunc("POST /result", h.handleResult)
	mux.HandleFunc("POST /call", h.handleCall)
	mux.HandleFunc("GET /tools", h.handleTools)
	mux.HandleFunc("POST /shutdown", h.handleShutdown)
	return h.guard(mux)
}

// guard rejects requests without the header or with a foreign Host, which
// also blocks DNS rebinding.
func (h *Hub) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(Header) == "" {
			http.Error(w, "missing "+Header+" header", http.StatusForbidden)
			return
		}
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil || (host != "127.0.0.1" && host != "localhost") {
			http.Error(w, "bad host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Serve listens on the loopback port until the hub is idle or shut down.
func (h *Hub) Serve(ctx context.Context) error {
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(h.port)))
	if err != nil {
		return err
	}
	server := &http.Server{Handler: h.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
			case <-h.shutdown:
			case <-ticker.C:
				if !h.idle() {
					continue
				}
			}
			shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = server.Shutdown(shutdownCtx)
			cancel()
			return
		}
	}()
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (h *Hub) idle() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.polling > 0 || len(h.inFlight) > 0 {
		return false
	}
	last := h.lastUse
	if h.lastPoll.After(last) {
		last = h.lastPoll
	}
	return time.Since(last) > IdleTimeout
}

func (h *Hub) touch() {
	h.mu.Lock()
	h.lastUse = time.Now()
	h.mu.Unlock()
}

// Status reports the hub and extension state.
func (h *Hub) Status() Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	return Status{
		Version:   h.version,
		Connected: h.connectedLocked(),
		Extension: h.hello,
		Revision:  h.revision,
	}
}

func (h *Hub) connectedLocked() bool {
	return h.hello != nil && (h.polling > 0 || time.Since(h.lastPoll) < ConnectedWindow)
}

func (h *Hub) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, h.Status())
}

func (h *Hub) handleHello(w http.ResponseWriter, r *http.Request) {
	var hello Hello
	if err := json.NewDecoder(r.Body).Decode(&hello); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	h.hello = &hello
	h.lastPoll = time.Now()
	h.revision++
	close(h.changed)
	h.changed = make(chan struct{})
	h.mu.Unlock()
	writeJSON(w, h.Status())
}

// handleNext hands the next call to the extension, or answers 204 after
// PollTimeout so the extension polls again.
func (h *Hub) handleNext(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	if h.hello == nil {
		h.mu.Unlock()
		// The hub restarted and forgot the extension: it must say hello again.
		http.Error(w, "say hello first", http.StatusConflict)
		return
	}
	h.polling++
	h.lastPoll = time.Now()
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.polling--
		h.lastPoll = time.Now()
		h.mu.Unlock()
	}()

	timer := time.NewTimer(PollTimeout)
	defer timer.Stop()
	for {
		select {
		case p := <-h.queue:
			h.mu.Lock()
			select {
			case <-p.picked:
				// The caller gave up before the extension took the call.
				h.mu.Unlock()
				continue
			default:
			}
			close(p.picked)
			h.inFlight[p.call.ID] = p
			h.mu.Unlock()
			writeJSON(w, p.call)
			return
		case <-timer.C:
			w.WriteHeader(http.StatusNoContent)
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (h *Hub) handleResult(w http.ResponseWriter, r *http.Request) {
	var res result
	if err := json.NewDecoder(r.Body).Decode(&res); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	p := h.inFlight[res.ID]
	delete(h.inFlight, res.ID)
	h.mu.Unlock()
	if p == nil {
		http.Error(w, "unknown call "+res.ID, http.StatusNotFound)
		return
	}
	p.done <- res.Result
	w.WriteHeader(http.StatusNoContent)
}

// CallTool sends a call to the extension and waits for its result.
func (h *Hub) CallTool(ctx context.Context, name string, arguments json.RawMessage) (json.RawMessage, error) {
	h.mu.Lock()
	if !h.connectedLocked() {
		h.mu.Unlock()
		return nil, ErrNotConnected
	}
	h.nextID++
	p := &pending{
		call:   Call{ID: strconv.Itoa(h.nextID), Name: name, Arguments: arguments},
		picked: make(chan struct{}),
		done:   make(chan json.RawMessage, 1),
	}
	h.lastUse = time.Now()
	h.mu.Unlock()

	select {
	case h.queue <- p:
	default:
		return nil, errors.New("too many calls waiting for Tiled")
	}

	pickup := time.NewTimer(PickupTimeout)
	defer pickup.Stop()
	select {
	case <-p.picked:
	case <-pickup.C:
		if h.abandon(p) {
			return nil, fmt.Errorf("tiled did not take the call within %s", PickupTimeout)
		}
		<-p.picked
	case <-ctx.Done():
		if h.abandon(p) {
			return nil, ctx.Err()
		}
		<-p.picked
	}

	wait := time.NewTimer(ResultTimeout)
	defer wait.Stop()
	select {
	case out := <-p.done:
		return out, nil
	case <-wait.C:
	case <-ctx.Done():
	}
	h.mu.Lock()
	delete(h.inFlight, p.call.ID)
	h.mu.Unlock()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, fmt.Errorf("tiled did not answer within %s", ResultTimeout)
}

// abandon marks a queued call as dropped; it reports false when the extension
// took the call first.
func (h *Hub) abandon(p *pending) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	select {
	case <-p.picked:
		return false
	default:
		close(p.picked)
		return true
	}
}

// ErrNotConnected means no extension is polling the hub.
var ErrNotConnected = errors.New("the Tiled extension is not connected")

func (h *Hub) handleCall(w http.ResponseWriter, r *http.Request) {
	var call Call
	if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	out, err := h.CallTool(r.Context(), call.Name, call.Arguments)
	if errors.Is(err, ErrNotConnected) {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusGatewayTimeout)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}

// handleTools answers at once when the client's revision is stale, otherwise
// waits for the tools to change or PollTimeout to pass.
func (h *Hub) handleTools(w http.ResponseWriter, r *http.Request) {
	h.touch()
	known, _ := strconv.Atoi(r.URL.Query().Get("revision"))
	h.mu.Lock()
	revision, changed := h.revision, h.changed
	h.mu.Unlock()
	if revision == known {
		timer := time.NewTimer(PollTimeout)
		defer timer.Stop()
		select {
		case <-changed:
		case <-timer.C:
		case <-r.Context().Done():
			return
		}
	}
	h.mu.Lock()
	list := ToolList{Revision: h.revision}
	if h.hello != nil {
		list.Tools = h.hello.Tools
	}
	h.mu.Unlock()
	writeJSON(w, list)
}

func (h *Hub) handleShutdown(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
	h.closeOnce.Do(func() { close(h.shutdown) })
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
