# tiled-mcp

Lets AI coding agents — Claude Code, Codex, Qwen Code — work with the maps open
in the [Tiled](https://www.mapeditor.org) map editor: inspect layers and
objects, check them against the project's classes, render the map to an image,
and add, move or change objects. Edits happen in the open editor, one undo step
each; the user sees them live and nothing touches the disk until the map is
saved.

## How it works

```
agent ⇄ stdio ⇄ tiled-mcp ⇄ hub on 127.0.0.1:47317 ⇄ tiled-mcp.js inside Tiled ⇄ open maps
```

Tiled's scripting engine can make HTTP requests but cannot listen on a port or
set timers. So the Tiled extension keeps a long-poll request open to a small
hub, runs the tool call that comes back with the Tiled Scripting API, posts the
result and polls again. The extension starts the hub itself when Tiled starts
(`tiled-mcp hub ensure`, a blocking call of a few milliseconds) and declares
its tools to it; every agent session's `tiled-mcp` forwards calls to the hub.

All map logic lives in the extension, on Tiled's own model: reading, undo,
rendering, project classes and the file format are Tiled's. The Go binary only
moves JSON.

## Install

Requires Tiled 1.12 (older versions lack parts of the scripting API the
extension uses), macOS or Linux (Windows: Claude Code only, through Git Bash),
`curl` and `tar`.

**Claude Code**

```
/plugin marketplace add shpaker/tiled-mcp
/plugin install tiled@tiled-mcp
```

**Codex**

```
codex plugin marketplace add shpaker/tiled-mcp
```

then install `tiled` from `/plugins`.

**Qwen Code**

```
qwen extensions install https://github.com/shpaker/tiled-mcp
```

On first start the plugin's launcher (`bin/tiled-mcp`) downloads the binary for
its version from the GitHub release into `~/.cache/tiled-mcp/<version>/` and
checks its sha256 against the release's `checksums.txt`.

Then ask the agent to work with a map. It calls `tiled_status`, and with your
consent `tiled_install_extension`, which writes the extension into Tiled's
global extensions folder; a running Tiled loads it without a restart.

## Tools

| Tool | |
|---|---|
| `tiled_status` | Is Tiled connected, which versions, what to do next |
| `tiled_install_extension` | Install or update the Tiled extension |
| `tiled_open` | Start Tiled or open a project and a map in it |
| `list_open_maps`, `map_summary` | Open maps; size, properties, layer tree |
| `list_objects`, `get_object` | Objects with geometry, class, properties (resolved with class defaults) |
| `list_property_types` | Project classes and enums |
| `validate` | Unknown classes, wrong property types, broken object references; optionally into Tiled's Issues view |
| `render_map` | PNG of the map as Tiled draws it |
| `add_object`, `update_object`, `delete_objects`, `add_object_layer`, `set_map_properties` | Edits, one undo step each, typed by the project's classes |
| `undo`, `save_map` | |
| `run_script` | Any Tiled Scripting API code as one undo step |

The extension also checks every map you save and lists problems in Tiled's
Issues view; clicking one selects the object.

## Security

The hub listens on 127.0.0.1 only and requires an `X-Tiled-MCP` header and a
loopback `Host`, so web pages cannot reach it. Any local process of your user
can; `run_script` runs arbitrary code in Tiled. Set another port with
`TILED_MCP_PORT`.

## Development

```
go test ./...
TILED_MCP_DEV=1 claude --plugin-dir .
```

`TILED_MCP_DEV=1` makes the launcher build from the checkout. Releases: bump
`VERSION` and the manifests (a test checks they agree), tag `v<version>`; CI
runs GoReleaser.

## License

MIT
