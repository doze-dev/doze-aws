/* The event-pattern builder: rows over a JSON textarea, kvEditor's contract.
 *
 * One pattern language backs both EventBridge rule/archive patterns and SNS
 * filter policies (the shared eventpattern matcher), so one builder serves
 * both. A row is a field path (dots descend into nested objects) holding one
 * or more conditions, OR'd within the field as the language defines; fields
 * AND together. Anything the rows cannot hold — $or, an operator the matcher
 * does not implement, a key containing a literal dot — hides the builder
 * instead of flattening it.
 *
 * Exact values keep their raw JSON in the row (a string "5" and a number 5
 * match differently under numeric conditions), so parse→serialize is
 * loss-free; typed input tries JSON first and falls back to a string.
 */
(function () {
  "use strict";

  var COND_TYPES = [
    { k: "value", label: "equals" },
    { k: "prefix", label: "prefix" },
    { k: "suffix", label: "suffix" },
    { k: "equals-ignore-case", label: "equals (any case)" },
    { k: "wildcard", label: "wildcard *" },
    { k: "anything-but", label: "anything but" },
    { k: "numeric", label: "numeric" },
    { k: "exists", label: "exists" },
    { k: "cidr", label: "cidr" },
  ];
  var NUM_OPS = { "<": 1, "<=": 1, "=": 1, ">": 1, ">=": 1 };

  function scalar(v) { return v === null || typeof v !== "object"; }

  function parseCond(item) {
    if (scalar(item)) return { type: "value", val: JSON.stringify(item) };
    if (Array.isArray(item)) return null;
    var keys = Object.keys(item);
    if (keys.length !== 1) return null;
    var op = keys[0], v = item[op];
    switch (op) {
      case "prefix": case "suffix": case "equals-ignore-case": case "wildcard": case "cidr":
        return typeof v === "string" ? { type: op, val: v } : null;
      case "exists":
        return typeof v === "boolean" ? { type: "exists", val: String(v) } : null;
      case "anything-but":
        return scalar(v) ? { type: "anything-but", val: JSON.stringify(v) } : null;
      case "numeric":
        if (!Array.isArray(v) || v.length < 2 || v.length % 2 !== 0) return null;
        for (var i = 0; i < v.length; i += 2) {
          if (!NUM_OPS[v[i]] || typeof v[i + 1] !== "number") return null;
        }
        return { type: "numeric", val: v.join(" ") };
      default:
        return null; // $or and everything else stays JSON
    }
  }

  function emitCond(c) {
    var parsed;
    switch (c.type) {
      case "value":
        try { return JSON.parse(c.val); } catch (e) { return c.val; }
      case "prefix": case "suffix": case "equals-ignore-case": case "wildcard": case "cidr":
        parsed = {}; parsed[c.type] = c.val; return parsed;
      case "exists":
        return { exists: c.val === "true" };
      case "anything-but":
        try { parsed = JSON.parse(c.val); } catch (e) { parsed = c.val; }
        return { "anything-but": parsed };
      case "numeric":
        var arr = [];
        c.val.trim().split(/\s+/).forEach(function (tok) {
          arr.push(NUM_OPS[tok] ? tok : parseFloat(tok));
        });
        return { numeric: arr };
    }
    return c.val;
  }

  window.patternBuilder = function (initial) {
    return {
      mode: "json", representable: false, dirty: false, ta: null,
      rows: [], condTypes: COND_TYPES,

      parse: function (text) {
        var t = (text || "").trim();
        if (!t) return [];
        if (t[0] !== "{") return null;
        var o;
        try { o = JSON.parse(t); } catch (e) { return null; }
        if (o === null || typeof o !== "object" || Array.isArray(o)) return null;
        var rows = [];
        var ok = (function walk(obj, path) {
          for (var k in obj) {
            if (!Object.prototype.hasOwnProperty.call(obj, k)) continue;
            if (k.indexOf(".") >= 0) return false; // the path coding would lie
            var p = path ? path + "." + k : k, v = obj[k];
            if (Array.isArray(v)) {
              if (!v.length) return false;
              var conds = [];
              for (var i = 0; i < v.length; i++) {
                var c = parseCond(v[i]);
                if (c === null) return false;
                conds.push(c);
              }
              rows.push({ path: p, conds: conds, draftType: "value", draftVal: "" });
            } else if (v !== null && typeof v === "object") {
              if (!walk(v, p)) return false;
            } else {
              return false; // a bare scalar is not a valid condition list
            }
          }
          return true;
        })(o, "");
        return ok ? rows : null;
      },

      serialize: function () {
        var root = {};
        this.rows.forEach(function (r) {
          if (!r.path || !r.conds.length) return;
          var parts = r.path.split(".");
          var node = root;
          for (var i = 0; i < parts.length - 1; i++) {
            if (typeof node[parts[i]] !== "object" || Array.isArray(node[parts[i]])) node[parts[i]] = {};
            node = node[parts[i]];
          }
          node[parts[parts.length - 1]] = r.conds.map(emitCond);
        });
        return JSON.stringify(root, null, 2);
      },

      condLabel: function (c) {
        if (c.type === "value") return c.val;
        if (c.type === "exists") return "exists: " + c.val;
        return c.type + ": " + c.val;
      },

      init: function (ta) {
        this.ta = ta;
        var rows = this.parse(initial);
        this.representable = rows !== null;
        if (rows !== null) { this.rows = rows; this.mode = "builder"; }
        if (!this.rows.length && this.representable) this.addRow();
      },
      read: function () { return this.ta && this.ta.__cm ? this.ta.__cm.getValue() : (this.ta ? this.ta.value : ""); },
      onText: function () { this.dirty = true; this.representable = this.parse(this.read()) !== null; },
      toBuilder: function () {
        var rows = this.parse(this.read());
        if (rows === null) return;
        this.rows = rows.length ? rows : [{ path: "", conds: [], draftType: "value", draftVal: "" }];
        this.mode = "builder";
      },
      toText: function () {
        this.mode = "json";
        var self = this;
        setTimeout(function () { if (window.dozeEditor) dozeEditor.refresh(self.ta.parentNode); }, 0);
      },
      sync: function () {
        this.dirty = true;
        var json = this.serialize();
        if (window.dozeEditor) dozeEditor.set(this.ta, json); else if (this.ta) this.ta.value = json;
        this.representable = true;
      },
      addRow: function () { this.rows.push({ path: "", conds: [], draftType: "value", draftVal: "" }); },
      dropRow: function (i) { this.rows.splice(i, 1); this.sync(); },
      commitCond: function (r) {
        var v = (r.draftVal || "").trim();
        if (r.draftType === "exists") v = v || "true";
        if (!v) return;
        r.conds.push({ type: r.draftType, val: v });
        r.draftVal = "";
        this.sync();
      },
      dropCond: function (r, i) { r.conds.splice(i, 1); this.sync(); },
    };
  };
})();
