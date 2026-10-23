package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Client talks to a hub from an agent session.
type Client struct {
	base string
	http *http.Client
}

// NewClient returns a client for the hub on the loopback port.
func NewClient(port int) *Client {
	return &Client{
		base: "http://127.0.0.1:" + strconv.Itoa(port),
		http: &http.Client{},
	}
}

// ErrNoHub means nothing listens on the hub port: Tiled is not running or the
// extension is not installed.
var ErrNoHub = errors.New("the tiled-mcp hub is not running")

// Status returns the hub state.
func (c *Client) Status(ctx context.Context) (Status, error) {
	var status Status
	err := c.do(ctx, http.MethodGet, "/status", nil, 5*time.Second, &status)
	return status, err
}

// Tools waits for a tool list newer than revision.
func (c *Client) Tools(ctx context.Context, revision int) (ToolList, error) {
	var list ToolList
	err := c.do(ctx, http.MethodGet, "/tools?revision="+strconv.Itoa(revision), nil, PollTimeout+10*time.Second, &list)
	return list, err
}

// Call runs a tool in the extension and returns its raw result.
func (c *Client) Call(ctx context.Context, name string, arguments json.RawMessage) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, http.MethodPost, "/call", Call{Name: name, Arguments: arguments}, PickupTimeout+ResultTimeout+10*time.Second, &out)
	return out, err
}

// Shutdown asks the hub to stop.
func (c *Client) Shutdown(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/shutdown", nil, 5*time.Second, nil)
}

func (c *Client) do(ctx context.Context, method, path string, body any, timeout time.Duration, out any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set(Header, "1")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		if isConnectionRefused(err) {
			return ErrNoHub
		}
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	switch {
	case response.StatusCode == http.StatusServiceUnavailable:
		return ErrNotConnected
	case response.StatusCode == http.StatusNoContent:
		return nil
	case response.StatusCode != http.StatusOK:
		return fmt.Errorf("hub: %s: %s", response.Status, strings.TrimSpace(string(data)))
	}
	if out == nil {
		return nil
	}
	if raw, ok := out.(*json.RawMessage); ok {
		*raw = data
		return nil
	}
	return json.Unmarshal(data, out)
}

func isConnectionRefused(err error) bool {
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	// Windows reports WSAECONNREFUSED, which is not syscall.ECONNREFUSED.
	message := err.Error()
	return strings.Contains(message, "connection refused") ||
		strings.Contains(message, "actively refused")
}
