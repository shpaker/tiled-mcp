/// <reference types="@mapeditor/tiled-api" />

/*
 * tiled-mcp: lets AI agents work with the maps open in Tiled.
 *
 * Tiled scripts can make HTTP requests but cannot listen on a port or set
 * timers. So this extension keeps a long-poll request open to the tiled-mcp
 * hub on 127.0.0.1, runs the tool call that comes back, posts the result and
 * polls again. The hub is started by `tiled-mcp hub ensure`, which returns as
 * soon as the hub listens.
 *
 * Installed by `tiled-mcp` itself (tool tiled_install_extension) together with
 * config.json next to this file.
 */

(function () {
  "use strict";

  const extensionDir = FileInfo.path(__filename);

  function readConfig() {
    const file = new TextFile(FileInfo.joinPaths(extensionDir, "config.json"), TextFile.ReadOnly);
    try {
      return JSON.parse(file.readAll());
    } finally {
      file.close();
    }
  }

  let config;
  try {
    config = readConfig();
  } catch (e) {
    tiled.error("tiled-mcp: cannot read config.json next to the extension: " + e);
    return;
  }

  const base = "http://127.0.0.1:" + config.port;

  // ================================================================ helpers

  const tools = {};

  function tool(name, description, properties, required, handler, annotations) {
    tools[name] = {
      name: name,
      description: description,
      inputSchema: { type: "object", properties: properties, required: required.length ? required : undefined },
      annotations: annotations,
      handler: handler,
    };
  }

  function toolList() {
    return Object.keys(tools).map(function (name) {
      const t = tools[name];
      return { name: t.name, description: t.description, inputSchema: t.inputSchema, annotations: t.annotations };
    });
  }

  const readOnly = { readOnlyHint: true };
  const mapArgument = {
    type: "string",
    description: "Map file path or file name. Defaults to the active map in Tiled.",
  };
  const propertiesArgument = {
    type: "object",
    description:
      "Custom properties to set: name → value. A number, string or boolean sets a plain value; " +
      '{"object": id} sets an object reference.',
    additionalProperties: true,
  };

  // findMap returns an open map by path or file name, opening it when needed.
  function findMap(name) {
    if (!name) {
      const active = tiled.activeAsset;
      if (active && active.isTileMap) return active;
      throw new Error("no map is active in Tiled; pass `map`");
    }
    const assets = tiled.openAssets;
    for (let i = 0; i < assets.length; i++) {
      const asset = assets[i];
      if (!asset.isTileMap) continue;
      if (asset.fileName === name || FileInfo.fileName(asset.fileName) === name) return asset;
    }
    const opened = tiled.open(name);
    if (opened && opened.isTileMap) return opened;
    throw new Error("map not found: " + name);
  }

  function layerType(layer) {
    if (layer.isTileLayer) return "tile";
    if (layer.isObjectLayer) return "object";
    if (layer.isImageLayer) return "image";
    if (layer.isGroupLayer) return "group";
    return "unknown";
  }

  // eachLayer visits every layer, group layers included, depth first.
  function eachLayer(container, visit) {
    for (let i = 0; i < container.layerCount; i++) {
      const layer = container.layerAt(i);
      visit(layer);
      if (layer.isGroupLayer) eachLayer(layer, visit);
    }
  }

  function objectLayers(map) {
    const layers = [];
    eachLayer(map, function (layer) {
      if (layer.isObjectLayer) layers.push(layer);
    });
    return layers;
  }

  function findObject(map, id) {
    const layers = objectLayers(map);
    for (let i = 0; i < layers.length; i++) {
      const objects = layers[i].objects;
      for (let k = 0; k < objects.length; k++) if (objects[k].id === id) return objects[k];
    }
    throw new Error("object " + id + " not found in " + FileInfo.fileName(map.fileName));
  }

  function findObjectLayer(map, name) {
    const layers = objectLayers(map);
    if (!name) {
      if (layers.length === 1) return layers[0];
      throw new Error(
        "pass `layer`: the map has " + layers.length + " object layers (" + layers.map(layerName).join(", ") + ")"
      );
    }
    for (let i = 0; i < layers.length; i++) if (layers[i].name === name) return layers[i];
    throw new Error("object layer not found: " + name + "; call add_object_layer first");
  }

  function layerName(layer) {
    return layer.name;
  }

  const shapeNames = ["rectangle", "polygon", "polyline", "ellipse", "capsule", "text", "point"];

  function shapeName(object) {
    return shapeNames[object.shape] || String(object.shape);
  }

  function shapeValue(name) {
    const index = shapeNames.indexOf(name);
    if (index < 0) throw new Error("unknown shape " + name + "; one of " + shapeNames.join(", "));
    return index;
  }

  function isMapObject(value) {
    return value && typeof value === "object" && "shape" in value && "id" in value;
  }

  // plain turns a property value into JSON: object references become
  // {"object": id}, custom-typed values {"type", "value"}.
  function plain(value, depth) {
    depth = depth || 0;
    if (value === null || value === undefined) return value;
    if (typeof value !== "object") return value;
    if (depth > 8) return String(value);
    if (Array.isArray(value)) {
      return value.map(function (v) {
        return plain(v, depth + 1);
      });
    }
    if (isMapObject(value)) return { object: value.id };
    if ("typeName" in value && "value" in value) return { type: value.typeName, value: plain(value.value, depth + 1) };
    if ("id" in value && Object.keys(value).length <= 1) return { object: value.id, missing: true };
    if ("url" in value) return String(value.url);
    const out = {};
    Object.keys(value).forEach(function (key) {
      out[key] = plain(value[key], depth + 1);
    });
    return out;
  }

  function plainProperties(object) {
    return plain(object.properties());
  }

  function describeObject(object, resolved) {
    const info = {
      id: object.id,
      name: object.name || undefined,
      class: object.className || undefined,
      layer: object.layer ? object.layer.name : undefined,
      shape: shapeName(object),
      x: object.x,
      y: object.y,
      width: object.width,
      height: object.height,
      rotation: object.rotation,
    };
    if (!object.visible) info.visible = false;
    if (object.polygon && object.polygon.length) {
      info.polygon = object.polygon.map(function (p) {
        return { x: p.x, y: p.y };
      });
    }
    if (object.tile) info.tile = { id: object.tile.id, tileset: object.tile.tileset ? object.tile.tileset.name : undefined };
    const properties = plainProperties(object);
    if (Object.keys(properties).length) info.properties = properties;
    if (resolved) {
      const all = defaultsFor(object.className);
      Object.keys(properties).forEach(function (key) {
        all[key] = properties[key];
      });
      if (Object.keys(all).length) info.resolvedProperties = all;
    }
    return info;
  }

  function propertyTypes() {
    const project = tiled.project;
    return project && project.propertyTypes ? project.propertyTypes : [];
  }

  function findType(name) {
    const types = propertyTypes();
    for (let i = 0; i < types.length; i++) if (types[i].name === name) return types[i];
    return null;
  }

  function defaultsFor(className) {
    const type = className ? findType(className) : null;
    return type && type.isClass ? plain(type.members) : {};
  }

  // memberTypes reads the class members' declared types from the project
  // file: the scripting API gives member values only, and a JS number cannot
  // tell int from float.
  function memberTypes(className) {
    const path = tiled.projectFilePath;
    if (!className || !path) return {};
    let project;
    try {
      const file = new TextFile(path, TextFile.ReadOnly);
      try {
        project = JSON.parse(file.readAll());
      } finally {
        file.close();
      }
    } catch (e) {
      return {};
    }
    const types = project.propertyTypes || [];
    for (let i = 0; i < types.length; i++) {
      if (types[i].name !== className || types[i].type !== "class") continue;
      const members = {};
      (types[i].members || []).forEach(function (member) {
        members[member.name] = { type: member.type, propertyType: member.propertyType };
      });
      return members;
    }
    return {};
  }

  // prepareProperties checks {name: value} against the class declared types
  // and returns a function that applies them. Checking first keeps a bad value
  // from leaving a half-done edit: errors inside map.macro do not propagate.
  // {"object": id} makes an object reference.
  function prepareProperties(map, className, properties) {
    const members = memberTypes(className);
    const setters = Object.keys(properties || {}).map(function (name) {
      const value = properties[name];
      const member = members[name];
      if (member && !member.propertyType && !compatible(value, member)) {
        throw new Error("property " + name + " of class " + className + " is " + member.type + ", got " + JSON.stringify(value));
      }
      if (value && typeof value === "object" && "object" in value) {
        const target = findObject(map, value.object);
        return function (object) {
          object.setProperty(name, target);
        };
      }
      return function (object) {
        if (member && member.propertyType) object.setProperty(name, tiled.propertyValue(member.propertyType, value));
        else if (member && member.type === "float") object.setFloatProperty(name, value);
        else if (member && member.type === "color") object.setColorProperty(name, value);
        else if (!member && typeof value === "number" && !Number.isInteger(value)) object.setFloatProperty(name, value);
        else object.setProperty(name, value);
      };
    });
    return function (object) {
      setters.forEach(function (set) {
        set(object);
      });
    };
  }

  // compatible tells whether a plain value fits a declared member type.
  function compatible(value, member) {
    switch (member.type) {
      case "int":
        return Number.isInteger(value);
      case "float":
        return typeof value === "number";
      case "bool":
        return typeof value === "boolean";
      case "string":
      case "file":
      case "color":
        return member.propertyType ? true : typeof value === "string";
      case "object":
        return value && typeof value === "object" && "object" in value;
      default:
        return true;
    }
  }

  function applyGeometry(object, args) {
    if (args.x !== undefined) object.x = args.x;
    if (args.y !== undefined) object.y = args.y;
    if (args.width !== undefined) object.width = args.width;
    if (args.height !== undefined) object.height = args.height;
    if (args.rotation !== undefined) object.rotation = args.rotation;
    if (args.polygon !== undefined) object.polygon = args.polygon;
  }

  // isOpen tells whether an asset is still open in Tiled without touching
  // it: a closed asset may already be deleted.
  function isOpen(asset) {
    return tiled.openAssets.indexOf(asset) >= 0;
  }

  // edit runs fn as one undo step and reports the map's problems. Tiled
  // swallows exceptions thrown inside a macro, so they are caught here.
  function edit(map, text, fn) {
    const fileName = map.fileName;
    let failure = null;
    const value = map.macro("tiled-mcp: " + text, function () {
      try {
        return fn();
      } catch (e) {
        failure = e;
      }
    });
    // A script can still close the map in a way run_script does not catch,
    // such as a Tiled action. The map is gone then and must not be touched.
    const closed = !isOpen(map);
    if (failure) {
      throw new Error(
        String(failure && failure.message ? failure.message : failure) +
          (closed
            ? " (the map was closed during the edit)"
            : " (changes made before the error stay in this undo step; call undo to revert them)")
      );
    }
    if (closed) {
      return { result: value, closed: fileName, next: "The map was closed during the edit without saving." };
    }
    const problems = validateMap(map);
    return {
      result: value,
      problems: problems.length ? problems.map(problemText) : undefined,
      saved: false,
      next: "Unsaved: call save_map when done, or the user saves in Tiled.",
    };
  }

  // ================================================================ validation

  function validateMap(map) {
    const problems = [];
    const ids = {};
    objectLayers(map).forEach(function (layer) {
      layer.objects.forEach(function (object) {
        ids[object.id] = true;
      });
    });

    function checkProperties(owner, label, className, properties, usage) {
      const type = className ? findType(className) : null;
      if (className && !type) {
        problems.push({ owner: owner, label: label, text: "unknown class " + JSON.stringify(className) });
      } else if (type && !type.isClass) {
        problems.push({ owner: owner, label: label, text: JSON.stringify(className) + " is an enum, not a class" });
      } else if (type && usage && type.usageFlags !== undefined && !(type.usageFlags & usage)) {
        problems.push({ owner: owner, label: label, text: "class " + JSON.stringify(className) + " is not meant for this kind of object" });
      }
      const defaults = type && type.isClass ? plain(type.members) : null;
      const members = type && type.isClass ? memberTypes(className) : {};
      Object.keys(properties).forEach(function (name) {
        const value = properties[name];
        if (value && typeof value === "object" && value.missing) {
          problems.push({ owner: owner, label: label, text: "property " + name + " refers to missing object " + value.object });
          return;
        }
        if (value && typeof value === "object" && "object" in value && !ids[value.object]) {
          problems.push({ owner: owner, label: label, text: "property " + name + " refers to missing object " + value.object });
          return;
        }
        if (!defaults || !(name in defaults)) return;
        if (members[name]) {
          if (!compatible(value, members[name])) {
            problems.push({
              owner: owner,
              label: label,
              text: "property " + name + " = " + JSON.stringify(value) + ", class " + className + " declares " + members[name].type,
            });
          }
          return;
        }
        const expected = defaults[name];
        if (expected === null || expected === undefined || typeof expected === "object") return;
        if (typeof value !== typeof expected) {
          problems.push({
            owner: owner,
            label: label,
            text: "property " + name + " is " + typeof value + ", class " + className + " expects " + typeof expected,
          });
        }
      });
    }

    checkProperties(map, "map", map.className, plainProperties(map), 0x001);
    eachLayer(map, function (layer) {
      checkProperties(layer, "layer " + JSON.stringify(layer.name), layer.className, plainProperties(layer), 0);
      if (!layer.isObjectLayer) return;
      layer.objects.forEach(function (object) {
        const label = "object " + object.id + (object.name ? " " + JSON.stringify(object.name) : "") + " in layer " + JSON.stringify(layer.name);
        checkProperties(object, label, object.className, plainProperties(object), 0x004);
      });
    });
    return problems;
  }

  function problemText(problem) {
    return problem.label + ": " + problem.text;
  }

  function select(map, owner) {
    tiled.activeAsset = map;
    if (isMapObject(owner)) {
      map.selectedObjects = [owner];
      const view = tiled.mapEditor && tiled.mapEditor.currentMapView;
      if (view) view.centerOn(owner.x, owner.y);
    }
  }

  // reportProblems shows the problems in Tiled's Issues view; clicking one
  // selects the object.
  function reportProblems(map, problems) {
    const file = FileInfo.fileName(map.fileName);
    problems.forEach(function (problem) {
      tiled.warn("tiled-mcp: " + file + ": " + problemText(problem), function () {
        select(map, problem.owner);
      });
    });
  }

  // ================================================================ tools

  tool(
    "map_summary",
    "Summary of a map in Tiled: size, tile size, class, properties, unsaved changes and the layer tree with object counts.",
    { map: mapArgument },
    [],
    function (args) {
      const map = findMap(args.map);
      function layers(container) {
        const out = [];
        for (let i = 0; i < container.layerCount; i++) {
          const layer = container.layerAt(i);
          const info = { id: layer.id, name: layer.name, type: layerType(layer) };
          if (!layer.visible) info.visible = false;
          if (layer.locked) info.locked = true;
          if (layer.className) info.class = layer.className;
          if (layer.offset && (layer.offset.x || layer.offset.y)) info.offset = { x: layer.offset.x, y: layer.offset.y };
          if (layer.isObjectLayer) info.objectCount = layer.objectCount;
          if (layer.isGroupLayer) info.layers = layers(layer);
          out.push(info);
        }
        return out;
      }
      return {
        fileName: map.fileName,
        modified: map.modified,
        width: map.width,
        height: map.height,
        tileWidth: map.tileWidth,
        tileHeight: map.tileHeight,
        infinite: map.infinite,
        class: map.className || undefined,
        properties: plainProperties(map),
        layers: layers(map),
      };
    },
    readOnly
  );

  tool(
    "list_open_maps",
    "Maps open in Tiled, the active one first, with unsaved-changes flags.",
    {},
    [],
    function () {
      const active = tiled.activeAsset;
      const maps = tiled.openAssets
        .filter(function (asset) {
          return asset.isTileMap;
        })
        .map(function (map) {
          return { fileName: map.fileName, active: map === active, modified: map.modified };
        });
      maps.sort(function (a, b) {
        return (b.active ? 1 : 0) - (a.active ? 1 : 0);
      });
      return { project: tiled.projectFilePath || undefined, maps: maps };
    },
    readOnly
  );

  tool(
    "list_objects",
    "Objects of a map with geometry in pixels (rotation in degrees, clockwise), class and own properties. Filters combine.",
    {
      map: mapArgument,
      layer: { type: "string", description: "Only this object layer." },
      class: { type: "string", description: "Only objects of this class." },
      name: { type: "string", description: "Only objects with this name." },
      resolved: { type: "boolean", description: "Also list properties with class defaults filled in." },
    },
    [],
    function (args) {
      const map = findMap(args.map);
      const out = [];
      objectLayers(map).forEach(function (layer) {
        if (args.layer && layer.name !== args.layer) return;
        layer.objects.forEach(function (object) {
          if (args.class && object.className !== args.class) return;
          if (args.name && object.name !== args.name) return;
          out.push(describeObject(object, args.resolved));
        });
      });
      return out;
    },
    readOnly
  );

  tool(
    "get_object",
    "One object by id, with properties resolved against its class defaults.",
    { map: mapArgument, id: { type: "integer" } },
    ["id"],
    function (args) {
      return describeObject(findObject(findMap(args.map), args.id), true);
    },
    readOnly
  );

  tool(
    "list_property_types",
    "Classes and enums of the current Tiled project: class members with default values, enum values.",
    {},
    [],
    function () {
      return {
        project: tiled.projectFilePath || undefined,
        types: propertyTypes().map(function (type) {
          if (type.isEnum) return { name: type.name, kind: "enum", values: type.values };
          return {
            name: type.name,
            kind: "class",
            members: plain(type.members),
            usageFlags: type.usageFlags,
            color: type.color ? String(type.color) : undefined,
          };
        }),
      };
    },
    readOnly
  );

  tool(
    "validate",
    "Checks a map against the project's classes: unknown classes, classes used for the wrong kind of object, " +
      "property types that differ from the class, references to missing objects. " +
      "With report=true the problems also go to Tiled's Issues view, where clicking one selects the object.",
    { map: mapArgument, report: { type: "boolean" } },
    [],
    function (args) {
      const map = findMap(args.map);
      const problems = validateMap(map);
      if (args.report) reportProblems(map, problems);
      return { fileName: map.fileName, ok: problems.length === 0, problems: problems.map(problemText) };
    },
    readOnly
  );

  tool(
    "render_map",
    "Renders a map as Tiled draws it — tile, object and image layers, without hidden layers — to a PNG image.",
    {
      map: mapArgument,
      size: { type: "integer", description: "Longest side of the image in pixels (default 1024)." },
    },
    [],
    function (args) {
      const map = findMap(args.map);
      const longest = args.size || 1024;
      // The scripting API has no pixel size of the map, and toImage(size)
      // letterboxes when the size has another aspect ratio. So render at the
      // size Tiled's renderer gives (any orientation, layer offsets, image
      // layers, infinite maps) and scale that.
      let image = map.toImage();
      if (image.width < 1 || image.height < 1) throw new Error("Tiled could not render the map");
      const scale = longest / Math.max(image.width, image.height);
      const width = Math.max(1, Math.round(image.width * scale));
      const height = Math.max(1, Math.round(image.height * scale));
      if (width !== image.width || height !== image.height) {
        image = image.scaled(width, height, Image.IgnoreAspectRatio, Image.SmoothTransformation);
      }
      return {
        content: [{ type: "image", mimeType: "image/png", data: Base64.encode(image.saveToData("PNG")) }],
      };
    },
    readOnly
  );

  const geometry = {
    x: { type: "number", description: "Pixels. For rectangles the top-left corner before rotation." },
    y: { type: "number" },
    width: { type: "number" },
    height: { type: "number" },
    rotation: { type: "number", description: "Degrees, clockwise, around (x, y)." },
    polygon: {
      type: "array",
      description: "Points relative to (x, y), for polygon and polyline shapes.",
      items: { type: "object", properties: { x: { type: "number" }, y: { type: "number" } }, required: ["x", "y"] },
    },
  };

  tool(
    "add_object",
    "Adds an object to an object layer as one undo step. Returns the new object's id and any problems found.",
    Object.assign(
      {
        map: mapArgument,
        layer: { type: "string", description: "Object layer; may be omitted when the map has only one." },
        class: { type: "string" },
        name: { type: "string" },
        shape: { type: "string", enum: shapeNames, description: "Default rectangle." },
        properties: propertiesArgument,
      },
      geometry
    ),
    ["x", "y"],
    function (args) {
      const map = findMap(args.map);
      const layer = findObjectLayer(map, args.layer);
      const shape = shapeValue(args.shape || "rectangle");
      const setProperties = prepareProperties(map, args.class, args.properties);
      return edit(map, "add object", function () {
        const object = new MapObject(shape, args.name || "");
        if (args.class) object.className = args.class;
        applyGeometry(object, args);
        layer.addObject(object);
        setProperties(object);
        return describeObject(object, false);
      });
    }
  );

  tool(
    "update_object",
    "Changes an object as one undo step: geometry, name, class, visibility and properties. Only given fields change.",
    Object.assign(
      {
        map: mapArgument,
        id: { type: "integer" },
        name: { type: "string" },
        class: { type: "string" },
        visible: { type: "boolean" },
        properties: propertiesArgument,
        removeProperties: { type: "array", items: { type: "string" } },
      },
      geometry
    ),
    ["id"],
    function (args) {
      const map = findMap(args.map);
      const object = findObject(map, args.id);
      const className = args.class !== undefined ? args.class : object.className;
      const setProperties = prepareProperties(map, className, args.properties);
      return edit(map, "update object " + args.id, function () {
        if (args.name !== undefined) object.name = args.name;
        if (args.class !== undefined) object.className = args.class;
        if (args.visible !== undefined) object.visible = args.visible;
        applyGeometry(object, args);
        setProperties(object);
        (args.removeProperties || []).forEach(function (name) {
          object.removeProperty(name);
        });
        return describeObject(object, false);
      });
    }
  );

  tool(
    "delete_objects",
    "Deletes objects by id as one undo step.",
    { map: mapArgument, ids: { type: "array", items: { type: "integer" } } },
    ["ids"],
    function (args) {
      const map = findMap(args.map);
      const objects = args.ids.map(function (id) {
        return findObject(map, id);
      });
      return edit(map, "delete objects", function () {
        objects.forEach(function (object) {
          object.layer.removeObject(object);
        });
        return { deleted: args.ids };
      });
    },
    { destructiveHint: true }
  );

  tool(
    "add_object_layer",
    "Adds an object layer on top of the map as one undo step.",
    { map: mapArgument, name: { type: "string" }, class: { type: "string" } },
    ["name"],
    function (args) {
      const map = findMap(args.map);
      return edit(map, "add object layer", function () {
        const layer = new ObjectGroup(args.name);
        if (args.class) layer.className = args.class;
        map.addLayer(layer);
        return { id: layer.id, name: layer.name };
      });
    }
  );

  tool(
    "set_map_properties",
    "Sets or removes the map's custom properties as one undo step.",
    { map: mapArgument, properties: propertiesArgument, remove: { type: "array", items: { type: "string" } } },
    [],
    function (args) {
      const map = findMap(args.map);
      const setProperties = prepareProperties(map, map.className, args.properties);
      return edit(map, "set map properties", function () {
        setProperties(map);
        (args.remove || []).forEach(function (name) {
          map.removeProperty(name);
        });
        return plainProperties(map);
      });
    }
  );

  tool(
    "undo",
    "Undoes the last steps on a map, as Edit > Undo in Tiled. Each tiled-mcp edit is one step.",
    { map: mapArgument, steps: { type: "integer", description: "Default 1." } },
    [],
    function (args) {
      const map = findMap(args.map);
      const steps = args.steps || 1;
      for (let i = 0; i < steps; i++) map.undo();
      return { undone: steps, modified: map.modified };
    }
  );

  tool(
    "save_map",
    "Saves a map open in Tiled to its file.",
    { map: mapArgument },
    [],
    function (args) {
      const map = findMap(args.map);
      if (!map.save()) throw new Error("Tiled could not save " + map.fileName);
      return { saved: map.fileName };
    }
  );

  // scriptTiled is the `tiled` a script sees. Closing the map whose undo step
  // the script runs in makes Tiled end the step on a deleted document and
  // crash, so tiled.close(map) only asks to close it after the step, and the
  // script cannot reopen it before then.
  function scriptTiled(map, closing) {
    return new Proxy(tiled, {
      get: function (target, key) {
        if (key === "close") {
          return function (asset) {
            if (asset !== map) return target.close(asset);
            closing.requested = true;
            return true;
          };
        }
        if (key === "open") {
          return function (fileName) {
            const asset = target.open(fileName);
            if (closing.requested && asset === map) {
              throw new Error(
                "the script closes " + FileInfo.fileName(map.fileName) + " when it ends, so it cannot reopen it; " +
                  "reopen it in another run_script call"
              );
            }
            return asset;
          };
        }
        return target[key];
      },
    });
  }

  tool(
    "run_script",
    "Runs JavaScript inside Tiled with the Tiled Scripting API (https://www.mapeditor.org/docs/scripting/). " +
      "`map` is the chosen map; the code runs as one undo step on it and its return value comes back as JSON. " +
      "The code can do anything Tiled scripts can, including reading and writing files and opening, saving and " +
      "closing other maps. `tiled.close(map)` closes `map` when the script ends, discarding unsaved changes; " +
      "the script cannot reopen it before then.",
    { map: mapArgument, code: { type: "string", description: "Function body, e.g. `return map.layerCount`." } },
    ["code"],
    function (args) {
      const map = findMap(args.map);
      const body = new Function("map", "tiled", args.code);
      // The undo step itself marks the map modified, so look before it.
      const modified = map.modified;
      const closing = { requested: false };
      const result = edit(map, "run script", function () {
        return plain(body(map, scriptTiled(map, closing)));
      });
      if (!closing.requested || !isOpen(map)) return result;
      const fileName = map.fileName;
      tiled.close(map);
      return {
        result: result.result,
        closed: fileName,
        next: modified ? "Closed without saving: its earlier unsaved changes are lost." : "Closed without saving.",
      };
    },
    { destructiveHint: true }
  );

  // ================================================================ calls

  function run(call) {
    const t = tools[call.name];
    if (!t) return errorResult("unknown tool " + call.name);
    try {
      return toResult(t.handler(call.arguments || {}));
    } catch (e) {
      return errorResult(String(e && e.message ? e.message : e));
    }
  }

  function toResult(value) {
    if (value && value.content) return value;
    if (typeof value === "string") return { content: [{ type: "text", text: value }] };
    return { content: [{ type: "text", text: JSON.stringify(value, null, 2) }] };
  }

  function errorResult(text) {
    return { isError: true, content: [{ type: "text", text: text }] };
  }

  // ================================================================ bridge

  let running = false;
  let failures = 0;

  function request(method, path, body, done) {
    const xhr = new XMLHttpRequest();
    xhr.onreadystatechange = function () {
      if (xhr.readyState === XMLHttpRequest.DONE) done(xhr);
    };
    xhr.open(method, base + path);
    xhr.setRequestHeader("X-Tiled-MCP", "1");
    if (body === undefined) {
      xhr.send();
    } else {
      xhr.setRequestHeader("Content-Type", "application/json");
      xhr.send(JSON.stringify(body));
    }
  }

  // ensureHub starts the hub when it is not running. It blocks Tiled only
  // until the hub listens, a few tens of milliseconds.
  function ensureHub() {
    const process = new Process();
    try {
      return process.exec(config.binary, ["hub", "ensure", "--port", String(config.port)], false) === 0;
    } catch (e) {
      return false;
    }
  }

  function stop(reason) {
    running = false;
    tiled.warn("tiled-mcp: " + reason + ". Will retry when you switch or open a map.");
  }

  // retry restarts the hub and says hello again. Without timers a dead hub
  // would make the loop spin, so after a few failures in a row it stops until
  // the next editor event.
  function retry() {
    failures++;
    if (failures > 3) {
      stop("lost the connection to the hub");
      return;
    }
    if (!ensureHub()) {
      stop("cannot start the hub at " + config.binary);
      return;
    }
    hello();
  }

  function hello() {
    const body = {
      version: config.version,
      tiled: tiled.version,
      project: tiled.projectFilePath || undefined,
      tools: toolList(),
    };
    request("POST", "/hello", body, function (xhr) {
      if (xhr.status === 200) poll();
      else retry();
    });
  }

  function poll() {
    request("GET", "/next", undefined, function (xhr) {
      if (xhr.status === 204) {
        failures = 0;
        poll();
        return;
      }
      if (xhr.status !== 200) {
        retry();
        return;
      }
      failures = 0;
      const call = JSON.parse(xhr.responseText);
      const result = run(call);
      request("POST", "/result", { id: call.id, result: result }, function () {
        poll();
      });
    });
  }

  function start() {
    if (running) return;
    running = true;
    failures = 0;
    if (!ensureHub()) {
      stop("cannot start the hub at " + config.binary);
      return;
    }
    hello();
  }

  // Checking on save shows the user the same problems the agent sees.
  tiled.assetSaved.connect(function (asset) {
    if (asset.isTileMap) reportProblems(asset, validateMap(asset));
  });

  tiled.activeAssetChanged.connect(start);
  tiled.assetOpened.connect(start);
  start();
})();
