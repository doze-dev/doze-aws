// builder.js — Alpine component factories.
//
// Loaded WITHOUT defer, deliberately. Alpine evaluates x-data before deferred
// scripts run, so a factory referenced by x-data must already exist when Alpine
// initialises. layout.html documents the same race for kvEditor and solves it
// by inlining; a non-deferred file is the same fix with the code somewhere you
// can read it. Order matters: this must come before alpine.min.js's deferred
// load, which every non-deferred script does by definition.
(function () {
  "use strict";

  /* dozeTags backs the tag editor: a DRAFT you submit, not controls that fire.
     rows is seeded from what the server rendered and is the only state; adding
     and removing touch it alone, and nothing reaches the service until Save.

     initial is deep-copied into both rows and base. Without the copy, reset()
     would restore references the edits had already mutated — Cancel would
     "restore" the values you were trying to abandon, which is worse than no
     Cancel at all. */
  window.dozeTags = function (initial) {
    var seed = (initial || []).map(function (t) { return { k: t.k, v: t.v }; });
    return {
      rows: seed.map(function (t) { return { k: t.k, v: t.v }; }),
      base: seed,
      dirty: false,
      reset: function () {
        this.rows = this.base.map(function (t) { return { k: t.k, v: t.v }; });
        this.dirty = false;
      },
    };
  };
})();

  /* rowsBuilder backs the flat-rule editors (S3 CORS and lifecycle): a JSON
     ARRAY of flat objects, projected as one row per rule. The schemas are
     doze-invented and unambiguous, so representability is simple: every key
     must be in the field spec and every value must match its kind. The
     textarea stays the source of truth, as everywhere.

     fields: [{key, kind}] with kind "text" | "num" | "list" (comma-joined in
     the row). Serialization omits empties, matching the server's omitempty. */
  window.rowsBuilder = function (initial, fields) {
    function blank() {
      var r = {};
      fields.forEach(function (f) { r[f.key] = ""; });
      return r;
    }
    return {
      mode: "json", rows: [], representable: false, dirty: false, ta: null,
      fields: fields,
      parse: function (text) {
        var t = (text || "").trim();
        if (!t) return [];
        if (t[0] !== "[") return null;
        var arr;
        try { arr = JSON.parse(t); } catch (e) { return null; }
        if (!Array.isArray(arr)) return null;
        var byKey = {};
        fields.forEach(function (f) { byKey[f.key] = f; });
        var rows = [];
        for (var i = 0; i < arr.length; i++) {
          var o = arr[i];
          if (o === null || typeof o !== "object" || Array.isArray(o)) return null;
          var row = blank();
          for (var k in o) {
            if (!Object.prototype.hasOwnProperty.call(o, k)) continue;
            var f = byKey[k], v = o[k];
            if (!f) return null;
            if (f.kind === "list") {
              if (!Array.isArray(v) || !v.every(function (x) { return typeof x === "string"; })) return null;
              row[k] = v.join(", ");
            } else if (f.kind === "num") {
              if (typeof v !== "number") return null;
              row[k] = String(v);
            } else {
              if (typeof v !== "string") return null;
              row[k] = v;
            }
          }
          rows.push(row);
        }
        return rows;
      },
      serialize: function () {
        var out = [];
        this.rows.forEach(function (row) {
          var o = {}, any = false;
          fields.forEach(function (f) {
            var v = (row[f.key] || "").trim();
            if (!v) return;
            if (f.kind === "list") {
              var l = v.split(",").map(function (x) { return x.trim(); }).filter(Boolean);
              if (l.length) { o[f.key] = l; any = true; }
            } else if (f.kind === "num") {
              var n = parseFloat(v);
              if (!isNaN(n) && n !== 0) { o[f.key] = n; any = true; }
            } else {
              o[f.key] = v; any = true;
            }
          });
          if (any) out.push(o);
        });
        return JSON.stringify(out, null, 2);
      },
      init: function (ta) {
        // Parse the factory's initial string, never the ref: under an htmx
        // swap Alpine can run x-init before $refs resolves, and every other
        // factory here already follows that rule.
        this.ta = ta;
        var rows = this.parse(initial);
        this.representable = rows !== null;
        if (rows !== null) { this.rows = rows; this.mode = "rows"; }
        if (this.representable && !this.rows.length) this.rows = [blank()];
      },
      read: function () { return this.ta && this.ta.__cm ? this.ta.__cm.getValue() : (this.ta ? this.ta.value : ""); },
      onText: function () { this.dirty = true; this.representable = this.parse(this.read()) !== null; },
      toRows: function () {
        var rows = this.parse(this.read());
        if (rows === null) return;
        this.rows = rows.length ? rows : [blank()];
        this.mode = "rows";
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
      addRow: function () { this.rows.push(blank()); },
      dropRow: function (i) { this.rows.splice(i, 1); if (!this.rows.length) this.rows.push(blank()); this.sync(); },
    };
  };

  /* cliPreview backs the copy-as-CLI affordance: a form annotates itself with
     the aws command it is about to be, and this renders that command LIVE
     from the form's current values. The microformat:
       $field        - the field's value, shell-quoted (empty stays empty)
       $field>text   - the literal text when the field is truthy (checkboxes)
       [ ... ]       - dropped whole when any $field inside resolves empty
       ENDPOINT      - the host the browser reached the console on, which is
                       the same listener the CLI would target
     Groups are lifted out to markers before any values go in, so a value
     containing '$' or ']' cannot re-trigger parsing. */
  window.cliPreview = function (tmpl) {
    return {
      open: false, cmd: "",
      q: function (v) {
        return /^[A-Za-z0-9_.:\/@=,*+-]+$/.test(v) ? v : "'" + v.replace(/'/g, "'\\''") + "'";
      },
      fieldVal: function (form, name) {
        var el = form && form.elements[name];
        if (!el) return "";
        if (el.type === "checkbox") return el.checked ? (el.value || "true") : "";
        return el.__cm ? el.__cm.getValue() : el.value;
      },
      subst: function (str, form, state) {
        var self = this;
        return str.replace(/\$([A-Za-z_][\w-]*)(>[^\s\]]+)?/g, function (m, name, lit) {
          var v = self.fieldVal(form, name);
          if (!v) { state.empty = true; return ""; }
          return lit ? lit.slice(1) : self.q(v);
        });
      },
      refresh: function () {
        var form = this.$el.closest("form");
        var s = tmpl.replace(/ENDPOINT/g, location.host);
        var groups = [];
        s = s.replace(/\[([^\]]*)\]/g, function (m, body) {
          groups.push(body);
          return "@@G" + (groups.length - 1) + "@@";
        });
        var outer = { empty: false };
        s = this.subst(s, form, outer);
        var self = this;
        s = s.replace(/@@G(\d+)@@/g, function (m, i) {
          var st = { empty: false };
          var sub = self.subst(groups[+i], form, st);
          return st.empty ? "" : sub;
        });
        this.cmd = ("aws --endpoint-url http://" + location.host + " " + s).replace(/\s+/g, " ").trim();
      },
      copyCmd: function () {
        if (navigator.clipboard) navigator.clipboard.writeText(this.cmd);
      },
      init: function () {
        var self = this, form = this.$el.closest("form");
        if (form) form.addEventListener("input", function () { if (self.open) self.refresh(); });
      },
    };
  };
