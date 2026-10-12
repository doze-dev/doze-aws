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
       $field@attr   - the named attribute of the selected <option>, for a menu
                       whose value is a key and whose parts the command needs
                       separately (a metric: namespace, name, dimensions)
       [ ... ]       - dropped whole when any $field inside resolves empty
       ENDPOINT      - the host the browser reached the console on, which is
                       the same listener the CLI would target
       newline       - starts a FURTHER command, for a form that makes more than
                       one call. It is shown only when every $field outside a
                       [ ... ] group has a value: the second call is made when its
                       field is set, so the preview stops showing it when it is not
       %doc($a,$b,…) - what the named builder (cliDocs below) makes of the form's
                       REPEATING rows: each $field is every control of that
                       name, in order, so the builder zips them the way the
                       handler zips the parallel arrays the form posts. It
                       yields shell words; none (no rows) counts as empty, so
                       a [ ... ] around it is dropped
     Groups are lifted out to markers before any values go in, so a value
     containing '$' or ']' cannot re-trigger parsing. */

  /* cliDocs are the arguments a flat template cannot spell: one document, or
     one list, built from rows the user adds and removes. Each mirrors the
     console's own call for the same form, so the previewed command makes what
     submitting makes. */
  var cliDocs = {
    /* DynamoDB's --attribute-definitions: every attribute a key schema names,
       the table's and each index's, once, in name order (client_ddb.go's
       CreateTable). Columns: hash key, its type, sort key, its type, then the
       index rows' name, hash, hash type, range, range type. */
    "ddb-attrs": function (c) {
      var defs = {};
      if (c[0][0]) defs[c[0][0]] = c[1][0] || "S";
      if (c[2][0]) defs[c[2][0]] = c[3][0] || "S";
      ddbIndexRows(c.slice(4)).forEach(function (g) {
        defs[g.hash] = g.hashType;
        if (g.range) defs[g.range] = g.rangeType;
      });
      return Object.keys(defs).sort().map(function (n) { return "AttributeName=" + n + ",AttributeType=" + defs[n]; });
    },
    /* DynamoDB's --global-secondary-indexes, as the one JSON document the CLI
       takes. Columns: the index rows' name, hash, hash type, range, range type. */
    "ddb-indexes": function (c) {
      var rows = ddbIndexRows(c).map(function (g) {
        var ks = [{ AttributeName: g.hash, KeyType: "HASH" }];
        if (g.range) ks.push({ AttributeName: g.range, KeyType: "RANGE" });
        return { IndexName: g.name, KeySchema: ks, Projection: { ProjectionType: "ALL" } };
      });
      return rows.length ? [JSON.stringify(rows)] : [];
    },
  };
  /* The index rows the create form sends: a row with no name or no partition
     key is skipped, as ddbCreateTable and CreateTable skip it. */
  function ddbIndexRows(c) {
    var out = [];
    (c[0] || []).forEach(function (name, i) {
      name = (name || "").trim();
      var hash = (c[1] && c[1][i]) || "";
      if (!name || !hash) return;
      out.push({
        name: name, hash: hash, hashType: (c[2] && c[2][i]) || "S",
        range: ((c[3] && c[3][i]) || "").trim(), rangeType: (c[4] && c[4][i]) || "S",
      });
    });
    return out;
  }

  window.cliPreview = function (tmpl) {
    return {
      open: false, cmd: "",
      q: function (v) {
        // Bare only when the shell cannot read it as anything else: no glob
        // (*), and not a leading - or = that the CLI or zsh would take for an
        // option or an =command expansion.
        return /^[A-Za-z0-9_.:\/@,+][A-Za-z0-9_.:\/@=,+-]*$/.test(v) ? v : "'" + v.replace(/'/g, "'\\''") + "'";
      },
      fieldVal: function (form, name, attr) {
        var el = form && form.elements[name];
        /* A control the form includes from elsewhere (hx-include) is part of
           what it submits, so it is part of what the preview shows. */
        if (!el && form && form.getAttribute("hx-include")) {
          try {
            document.querySelectorAll(form.getAttribute("hx-include")).forEach(function (n) {
              if (el) return;
              el = n.name === name ? n : (n.querySelector && n.querySelector('[name="' + name + '"]'));
            });
          } catch (e) { /* a selector hx-include accepts that querySelectorAll does not */ }
        }
        if (!el || el.disabled) return ""; // a disabled control is not submitted
        if (attr) {
          var opt = el.options && el.options[el.selectedIndex];
          return (opt && opt.getAttribute(attr)) || "";
        }
        if (el.type === "checkbox") return el.checked ? (el.value || "true") : "";
        return el.__cm ? el.__cm.getValue() : el.value;
      },
      /* A substituted value is parked in state.vals and stands in the string
         as a marker until the command's own whitespace has been tidied: a
         value's spaces and newlines are the user's (a message body, a secret)
         and collapsing them would show a command that sends something else. */
      /* every enabled control of that name, in document order: a repeating
         row's column. */
      fieldVals: function (form, name) {
        if (!form) return [];
        return Array.prototype.filter.call(form.querySelectorAll('[name="' + name + '"]'), function (el) {
          return !el.disabled;
        }).map(function (el) {
          if (el.type === "checkbox") return el.checked ? (el.value || "true") : "";
          return el.__cm ? el.__cm.getValue() : el.value;
        });
      },
      subst: function (str, form, state) {
        var self = this;
        str = str.replace(/@@D(\d+)@@/g, function (m, i) {
          var d = state.docs[+i], build = cliDocs[d.name];
          var words = build ? build(d.fields.map(function (f) { return self.fieldVals(form, f); })) : [];
          if (!words.length) { state.empty = true; return ""; }
          return words.map(function (w) {
            state.vals.push(self.q(w));
            return "\u0001" + (state.vals.length - 1) + "\u0001";
          }).join(" ");
        });
        return str.replace(/\$([A-Za-z_][\w-]*)(@[\w-]+)?(>[^\s\]]+)?/g, function (m, name, attr, lit) {
          var v = self.fieldVal(form, name, attr ? attr.slice(1) : "");
          /* $field>text asks "is it on": a flag bound from the page's state
             arrives as the word false when it is off. */
          if (lit && v === "false") v = "";
          if (!v) { state.empty = true; return ""; }
          state.vals.push(lit ? lit.slice(1) : self.q(v));
          return "\u0001" + (state.vals.length - 1) + "\u0001";
        });
      },
      /* render one command. empty reports whether a $field outside a group had
         no value, which is what drops a FURTHER command and not the first. */
      render: function (src, form) {
        /* Only as the host of a URL: a queue called MY_ENDPOINT_Q is a name. */
        var s = src.replace(/http:\/\/ENDPOINT\b/g, "http://" + location.host);
        /* Documents first, so the $fields in their argument list are the
           builder's columns and not values to substitute. */
        var docs = [];
        s = s.replace(/%([a-z][\w-]*)\(([^)]*)\)/g, function (m, name, args) {
          docs.push({ name: name, fields: args.split(",").map(function (a) { return a.trim().replace(/^\$/, ""); }) });
          return "@@D" + (docs.length - 1) + "@@";
        });
        var groups = [];
        s = s.replace(/\[([^\]]*)\]/g, function (m, body) {
          groups.push(body);
          return "@@G" + (groups.length - 1) + "@@";
        });
        var vals = [];
        var outer = { empty: false, vals: vals, docs: docs };
        s = this.subst(s, form, outer);
        var self = this;
        s = s.replace(/@@G(\d+)@@/g, function (m, i) {
          var st = { empty: false, vals: vals, docs: docs };
          var sub = self.subst(groups[+i], form, st);
          return st.empty ? "" : sub;
        });
        s = s.replace(/\s+/g, " ").trim().replace(/\u0001(\d+)\u0001/g, function (m, i) { return vals[+i]; });
        return { text: s, empty: outer.empty };
      },
      refresh: function () {
        var form = this.$el.closest("form"), self = this, lines = [];
        tmpl.split("\n").forEach(function (src, i) {
          var r = self.render(src, form);
          if (i > 0 && r.empty) return;
          lines.push("aws --endpoint-url http://" + location.host + " " + r.text);
        });
        this.cmd = lines.join("\n");
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
