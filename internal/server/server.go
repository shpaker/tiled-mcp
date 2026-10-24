// Package server is the MCP server an agent starts over stdio. Its own tools
// work without Tiled; the rest are declared by the Tiled extension and proxied
// through the hub.
package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shpaker/tiled-mcp/extension"
	"github.com/shpaker/tiled-mcp/internal/hub"
)

// Options configure the server.
type Options struct {
	Version string
	Port    int
	// Binary is the tiled-mcp executable the extension starts the hub with.
	Binary string
}

// Server is the MCP server of one agent session.
type Server struct {
	options Options
	client  *hub.Client
	mcp     *mcp.Server

	mu          sync.Mutex
	extension   []string // names of the tools declared by the extension
	updatedFrom string   // version of the extension this server replaced
}

// New builds the server with its own tools.
func New(options Options) *Server {
	s := &Server{
		options: options,
		client:  hub.NewClient(options.Port),
		mcp: mcp.NewServer(&mcp.Implementation{Name: "tiled-mcp", Version: options.Version}, &mcp.ServerOptions{
			Instructions: instructions,
		}),
	}
	s.addOwnTools()
	return s
}

const instructions = `Works with the maps open in the Tiled map editor through a Tiled extension.
Call tiled_status first. If the extension is missing, ask the user and call tiled_install_extension (an older one is updated automatically); if Tiled is not running, call tiled_open.
Edits happen in the open editor with undo; nothing is written to disk until the map is saved.`

// Run serves MCP over stdio until the agent disconnects.
func (s *Server) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// A running Tiled reloads an updated extension, which then restarts the
	// hub from this server's binary.
	s.updateExtension()
	// Take the extension's tools before serving, so the agent's first
	// tools/list already has them when Tiled is running.
	revision := -1
	if list, err := s.client.Tools(ctx, revision); err == nil {
		revision = list.Revision
		s.setExtensionTools(list.Tools)
	}
	go s.syncTools(ctx, revision)
	return s.mcp.Run(ctx, &mcp.StdioTransport{})
}

// syncTools mirrors the extension's tools. It waits for changes with a long
// poll and, while the hub is down, retries every few seconds.
func (s *Server) syncTools(ctx context.Context, revision int) {
	for ctx.Err() == nil {
		list, err := s.client.Tools(ctx, revision)
		if err != nil {
			if errors.Is(err, hub.ErrNoHub) {
				s.setExtensionTools(nil)
				revision = -1
			} else if ctx.Err() == nil {
				log.Printf("tiled-mcp: tools: %v", err)
			}
			select {
			case <-ctx.Done():
			case <-time.After(3 * time.Second):
			}
			continue
		}
		if list.Revision != revision {
			revision = list.Revision
			s.setExtensionTools(list.Tools)
		}
	}
}

func (s *Server) setExtensionTools(tools []hub.Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name)
	}
	var stale []string
	for _, name := range s.extension {
		if !slices.Contains(names, name) {
			stale = append(stale, name)
		}
	}
	if len(stale) > 0 {
		s.mcp.RemoveTools(stale...)
	}
	for _, t := range tools {
		tool := &mcp.Tool{
			Name:        t.Name,
			Title:       t.Title,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}
		if len(t.Annotations) > 0 {
			var annotations mcp.ToolAnnotations
			if err := json.Unmarshal(t.Annotations, &annotations); err == nil {
				tool.Annotations = &annotations
			}
		}
		name := t.Name
		s.mcp.AddTool(tool, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return s.proxy(ctx, name, request.Params.Arguments)
		})
	}
	s.extension = names
}

func (s *Server) proxy(ctx context.Context, name string, arguments json.RawMessage) (*mcp.CallToolResult, error) {
	out, err := s.client.Call(ctx, name, arguments)
	switch {
	case errors.Is(err, hub.ErrNoHub), errors.Is(err, hub.ErrNotConnected):
		return errorResult("Tiled is not connected (%v). Call tiled_status.", err), nil
	case err != nil:
		return errorResult("%v", err), nil
	}
	return decodeResult(out)
}

// wireResult is a tool result as the extension sends it.
type wireResult struct {
	IsError bool `json:"isError"`
	Content []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Data     string `json:"data"`
		MIMEType string `json:"mimeType"`
	} `json:"content"`
}

func decodeResult(data json.RawMessage) (*mcp.CallToolResult, error) {
	var wire wireResult
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, fmt.Errorf("bad result from the extension: %w", err)
	}
	result := &mcp.CallToolResult{IsError: wire.IsError}
	for _, c := range wire.Content {
		switch c.Type {
		case "image":
			image, err := base64.StdEncoding.DecodeString(c.Data)
			if err != nil {
				return nil, fmt.Errorf("bad image from the extension: %w", err)
			}
			result.Content = append(result.Content, &mcp.ImageContent{Data: image, MIMEType: c.MIMEType})
		default:
			result.Content = append(result.Content, &mcp.TextContent{Text: c.Text})
		}
	}
	return result, nil
}

func errorResult(format string, args ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}},
	}
}

func textResult(value any) *mcp.CallToolResult {
	data, _ := json.MarshalIndent(value, "", "  ")
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}
}

func (s *Server) addOwnTools() {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "tiled_status",
		Description: "Whether Tiled runs with the tiled-mcp extension connected: versions, project and what to do next.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		return textResult(s.status(ctx)), nil, nil
	})

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "tiled_install_extension",
		Description: "Installs or updates the tiled-mcp extension in Tiled's global extensions folder. " +
			"A running Tiled loads it at once. Ask the user before calling.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		path, err := extension.Install(extension.Config{
			Version: s.options.Version,
			Binary:  s.options.Binary,
			Port:    s.options.Port,
		})
		if err != nil {
			return errorResult("install failed: %v", err), nil, nil
		}
		return textResult(map[string]string{
			"installed": path,
			"next":      "If Tiled is running it reloads its scripts now; call tiled_status in a few seconds.",
		}), nil, nil
	})

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "tiled_open",
		Description: "Starts Tiled, or brings it to the front, optionally opening a map or project file.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in openInput) (*mcp.CallToolResult, any, error) {
		// The project goes first: Tiled switches to it, then opens the map in it.
		var files []string
		for _, path := range []string{in.Project, in.Path} {
			if path == "" {
				continue
			}
			absolute, err := filepath.Abs(path)
			if err != nil {
				return errorResult("%v", err), nil, nil
			}
			if _, err := os.Stat(absolute); err != nil {
				return errorResult("%v", err), nil, nil
			}
			files = append(files, absolute)
		}
		if err := openTiled(files); err != nil {
			return errorResult("cannot start Tiled: %v", err), nil, nil
		}
		return textResult(map[string]string{
			"started": "Tiled",
			"next":    "The extension connects once Tiled has loaded; call tiled_status in a few seconds.",
		}), nil, nil
	})
}

type openInput struct {
	Project string `json:"project,omitempty" jsonschema:"project (.tiled-project) to switch to, so maps get its classes"`
	Path    string `json:"path,omitempty" jsonschema:"map (.tmj, .tmx) to open"`
}

type status struct {
	Server    string          `json:"server"`
	Hub       string          `json:"hub"`
	Connected bool            `json:"connected"`
	Tiled     string          `json:"tiled,omitempty"`
	Project   string          `json:"project,omitempty"`
	Extension *installedState `json:"extension"`
	Tools     []string        `json:"tools,omitempty"`
	Next      string          `json:"next,omitempty"`
}

type installedState struct {
	Path      string `json:"path"`
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	Current   bool   `json:"current"`
	// Updated is set when this server replaced an older extension.
	Updated string `json:"updated,omitempty"`
}

func (s *Server) status(ctx context.Context) status {
	result := status{Server: s.options.Version}

	// Another agent with an older tiled-mcp may have installed its extension
	// since this server started.
	s.updateExtension()
	config, path, err := extension.Installed()
	result.Extension = &installedState{Path: path, Installed: err == nil, Version: config.Version}
	result.Extension.Current = err == nil && config.Version == s.options.Version && config.Binary == s.options.Binary
	s.mu.Lock()
	if s.updatedFrom != "" {
		result.Extension.Updated = s.updatedFrom + " → " + s.options.Version
	}
	s.mu.Unlock()
	newer, _ := olderVersion(s.options.Version, config.Version)

	hubStatus, err := s.client.Status(ctx)
	switch {
	case errors.Is(err, hub.ErrNoHub):
		result.Hub = "not running"
	case err != nil:
		result.Hub = err.Error()
	default:
		result.Hub = hubStatus.Version
		result.Connected = hubStatus.Connected
		if hubStatus.Extension != nil {
			result.Tiled = hubStatus.Extension.Tiled
			result.Project = hubStatus.Extension.Project
			for _, t := range hubStatus.Extension.Tools {
				result.Tools = append(result.Tools, t.Name)
			}
		}
	}

	switch {
	case !result.Extension.Installed:
		result.Next = "The extension is not installed: ask the user, then call tiled_install_extension."
	case newer:
		result.Next = fmt.Sprintf("The extension is from a newer tiled-mcp (%s) than this server (%s): "+
			"suggest the user update the plugin; do not call tiled_install_extension.", config.Version, s.options.Version)
	case !result.Extension.Current:
		result.Next = "The extension is from another tiled-mcp: ask the user, then call tiled_install_extension to update it."
	case !result.Connected:
		result.Next = "Tiled is not running or has not loaded the extension: call tiled_open, or check Tiled's Console view."
	}
	return result
}
