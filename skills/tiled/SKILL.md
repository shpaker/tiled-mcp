---
name: tiled
description: Work with maps open in the Tiled map editor through the tiled MCP tools — inspect layers and objects, check them against the project's classes, render the map to see it, and add, move or change objects with undo. Use when the user asks to look at, build, fix or check a Tiled level (.tmj, .tmx, .tiled-project).
---

# Tiled maps

The `tiled` MCP tools work inside the running Tiled editor: the agent and the
user see and edit the same open map, every edit is one undo step, and nothing
reaches the disk until the map is saved.

## Loop

1. **Connect.** `tiled_status`. If the extension is missing, ask the user, then
   `tiled_install_extension`; an older one updates itself, and for a newer one
   follow `next`. If Tiled is not connected,
   `tiled_open` with the project (`.tiled-project`) and the map, wait a few
   seconds, check `tiled_status` again.
2. **Read.** `list_open_maps`, `map_summary`, `list_property_types` (what
   classes exist and their defaults), `list_objects` / `get_object`. Look for
   the project's own level conventions (for example a `docs/levels.md`) before
   placing anything: classes, units and axes are project decisions.
3. **Edit.** `add_object`, `update_object`, `delete_objects`,
   `add_object_layer`, `set_map_properties`; `run_script` for anything else
   the Tiled Scripting API can do. Each result lists the map's problems.
4. **Check.** `validate` for classes and properties, `render_map` to see the
   result. Fix and repeat; `undo` reverts a step.
5. **Save.** `save_map` when the user wants the change on disk, or leave it
   unsaved for them to review in Tiled — say which.

## Geometry

- Pixels of the map, y down. Rotation in degrees, clockwise.
- A rectangle's `x`, `y` is its top-left corner before rotation, and it rotates
  around that corner. A point object has no size.
- Polygon and polyline points are relative to the object's `x`, `y`.

## Care

- Properties are typed by the class in the project; a value of the wrong type
  is rejected before the edit. `{"object": id}` sets an object reference.
- A map with unsaved changes in Tiled is not reloaded from disk: edit open maps
  only through these tools, not by writing the file.
- `run_script` runs arbitrary code in Tiled; prefer the dedicated tools.
