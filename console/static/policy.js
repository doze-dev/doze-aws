/* The IAM policy builder: rows over a textarea that stays the source of truth.
 *
 * The contract is kvEditor's, with a richer row: the JSON textarea keeps
 * submitting, the builder is a PROJECTION rebuilt on entry and re-serialized
 * on every change, and a document the rows cannot represent hides the builder
 * tab instead of lossily flattening. ParsePolicy on the server is deliberately
 * shallow ("stricter locally would fail policies that work in the cloud"), so
 * the builder never rejects a document either — anything it cannot hold just
 * stays JSON.
 *
 * Two shapes are preserved, not normalized, because rewriting them would diff
 * a document the user never edited:
 *   - a single-element Action/Resource/value list re-serializes as the bare
 *     string, which is also the canonical form the service itself re-marshals;
 *   - a bare (non-array) Statement stays bare while it is still exactly one.
 */
(function () {
  "use strict";

  /* Exactly the operators applyOperator implements — an unknown operator
     evaluates to false server-side, so offering more would be offering
     conditions that can never pass. */
  var OPERATORS = [
    "StringEquals", "StringNotEquals", "StringEqualsIgnoreCase", "StringNotEqualsIgnoreCase",
    "StringLike", "StringNotLike",
    "ArnEquals", "ArnNotEquals", "ArnLike", "ArnNotLike",
    "Bool",
    "NumericEquals", "NumericNotEquals", "NumericLessThan", "NumericLessThanEquals",
    "NumericGreaterThan", "NumericGreaterThanEquals",
    "DateEquals", "DateNotEquals", "DateLessThan", "DateLessThanEquals",
    "DateGreaterThan", "DateGreaterThanEquals",
    "IpAddress", "NotIpAddress",
  ];

  /* The seven context keys the authorizer actually populates. Offering AWS's
     full catalog would be offering keys that can never match locally. */
  var CONTEXT_KEYS = [
    "aws:PrincipalAccount", "aws:PrincipalArn", "aws:RequestedRegion",
    "aws:SecureTransport", "aws:username", "aws:SourceIp", "aws:UserAgent",
  ];

  var PRINCIPAL_TYPES = ["AWS", "Service", "Federated", "CanonicalUser"];

  function isStr(v) { return typeof v === "string"; }

  /* strList: accept string | string[] of strings; null = unrepresentable. */
  function strList(v) {
    if (isStr(v)) return { vals: [v], bare: true };
    if (Array.isArray(v) && v.every(isStr)) return { vals: v.slice(), bare: false };
    return null;
  }

  function emitList(vals) { return vals.length === 1 ? vals[0] : vals; }

  function newStatement() {
    return {
      sid: "", effect: "Allow",
      actions: [], notAction: false, actionDraft: "",
      resources: ["*"], notResource: false, resourceDraft: "",
      pmode: "none", pnot: false, prows: [], // pmode: none | any | typed
      conds: [],
    };
  }

  window.policyBuilder = function (initial) {
    return {
      mode: "json", representable: false, dirty: false, ta: null,
      version: "2012-10-17", docId: "", bareStmt: false, stmts: [],
      operators: OPERATORS, contextKeys: CONTEXT_KEYS, principalTypes: PRINCIPAL_TYPES,

      parse: function (text) {
        var t = (text || "").trim();
        if (!t || t[0] !== "{") return null;
        var o;
        try { o = JSON.parse(t); } catch (e) { return null; }
        if (o === null || typeof o !== "object" || Array.isArray(o)) return null;
        var meta = { version: "2012-10-17", docId: "", bareStmt: false, stmts: [] };
        for (var k in o) {
          if (!Object.prototype.hasOwnProperty.call(o, k)) continue;
          if (k === "Version") { if (!isStr(o[k])) return null; meta.version = o[k]; continue; }
          if (k === "Id") { if (!isStr(o[k])) return null; meta.docId = o[k]; continue; }
          if (k !== "Statement") return null; // a key the rows would drop
        }
        var raw = o.Statement;
        if (raw === undefined) return null;
        if (!Array.isArray(raw)) { meta.bareStmt = true; raw = [raw]; }
        for (var i = 0; i < raw.length; i++) {
          var st = this.parseStatement(raw[i]);
          if (st === null) return null;
          meta.stmts.push(st);
        }
        if (!meta.stmts.length) return null;
        return meta;
      },

      parseStatement: function (s) {
        if (s === null || typeof s !== "object" || Array.isArray(s)) return null;
        var out = newStatement();
        out.resources = [];
        for (var k in s) {
          if (!Object.prototype.hasOwnProperty.call(s, k)) continue;
          var v = s[k], l;
          switch (k) {
            case "Sid": if (!isStr(v)) return null; out.sid = v; break;
            case "Effect":
              if (v !== "Allow" && v !== "Deny") return null;
              out.effect = v; break;
            case "Action": case "NotAction":
              if (out.actions.length) return null; // both present — rows hold one
              l = strList(v); if (!l) return null;
              out.actions = l.vals; out.notAction = k === "NotAction"; break;
            case "Resource": case "NotResource":
              if (out.resources.length) return null;
              l = strList(v); if (!l) return null;
              out.resources = l.vals; out.notResource = k === "NotResource"; break;
            case "Principal": case "NotPrincipal":
              if (out.pmode !== "none") return null;
              out.pnot = k === "NotPrincipal";
              if (v === "*") { out.pmode = "any"; break; }
              if (v === null || typeof v !== "object" || Array.isArray(v)) return null;
              out.pmode = "typed";
              for (var pt in v) {
                if (!Object.prototype.hasOwnProperty.call(v, pt)) continue;
                l = strList(v[pt]); if (!l) return null;
                out.prows.push({ type: pt, vals: l.vals.join(", ") });
              }
              if (!out.prows.length) return null;
              break;
            case "Condition":
              if (v === null || typeof v !== "object" || Array.isArray(v)) return null;
              for (var op in v) {
                if (!Object.prototype.hasOwnProperty.call(v, op)) continue;
                var parts = this.splitOperator(op);
                if (!parts) return null;
                var keys = v[op];
                if (keys === null || typeof keys !== "object" || Array.isArray(keys)) return null;
                var any = false;
                for (var ck in keys) {
                  if (!Object.prototype.hasOwnProperty.call(keys, ck)) continue;
                  l = strList(keys[ck]); if (!l) return null;
                  out.conds.push({ mod: parts.mod, op: parts.op, ifExists: parts.ifExists, key: ck, vals: l.vals.join(", ") });
                  any = true;
                }
                if (!any) return null;
              }
              break;
            default:
              return null;
          }
        }
        if (!out.actions.length) return null; // ParsePolicy requires one of Action/NotAction
        return out;
      },

      /* splitOperator honors the two prefixes and one suffix the evaluator
         strips; anything left that is not an implemented operator is
         unrepresentable (it would silently evaluate false). */
      splitOperator: function (op) {
        var mod = "";
        if (op.indexOf("ForAllValues:") === 0) { mod = "ForAllValues:"; op = op.slice(13); }
        else if (op.indexOf("ForAnyValue:") === 0) { mod = "ForAnyValue:"; op = op.slice(12); }
        var ifExists = false;
        if (op.length > 8 && op.slice(-8) === "IfExists") { ifExists = true; op = op.slice(0, -8); }
        if (OPERATORS.indexOf(op) < 0) return null;
        return { mod: mod, op: op, ifExists: ifExists };
      },

      serialize: function () {
        var self = this;
        var stmts = this.stmts.map(function (st) { return self.emitStatement(st); });
        var doc = {};
        if (this.version) doc.Version = this.version;
        if (this.docId) doc.Id = this.docId;
        doc.Statement = (this.bareStmt && stmts.length === 1) ? stmts[0] : stmts;
        return JSON.stringify(doc, null, 2);
      },

      emitStatement: function (st) {
        var s = {};
        if (st.sid) s.Sid = st.sid;
        s.Effect = st.effect;
        var acts = st.actions.filter(Boolean);
        if (acts.length) s[st.notAction ? "NotAction" : "Action"] = emitList(acts);
        var res = st.resources.filter(Boolean);
        if (res.length) s[st.notResource ? "NotResource" : "Resource"] = emitList(res);
        if (st.pmode === "any") s[st.pnot ? "NotPrincipal" : "Principal"] = "*";
        if (st.pmode === "typed") {
          var p = {};
          st.prows.forEach(function (r) {
            var vals = r.vals.split(",").map(function (x) { return x.trim(); }).filter(Boolean);
            if (r.type && vals.length) p[r.type] = emitList(vals);
          });
          if (Object.keys(p).length) s[st.pnot ? "NotPrincipal" : "Principal"] = p;
        }
        if (st.conds.length) {
          var c = {};
          st.conds.forEach(function (r) {
            if (!r.key) return;
            var full = r.mod + r.op + (r.ifExists ? "IfExists" : "");
            var vals = r.vals.split(",").map(function (x) { return x.trim(); }).filter(Boolean);
            if (!vals.length) return;
            if (!c[full]) c[full] = {};
            c[full][r.key] = emitList(vals);
          });
          if (Object.keys(c).length) s.Condition = c;
        }
        return s;
      },

      init: function (ta) {
        this.ta = ta;
        var meta = this.parse(initial);
        this.representable = meta !== null;
        if (meta) {
          this.version = meta.version; this.docId = meta.docId;
          this.bareStmt = meta.bareStmt; this.stmts = meta.stmts;
          this.mode = "builder";
        }
        if (!(initial || "").trim()) {
          this.stmts = [newStatement()];
          this.representable = true; this.mode = "builder";
        }
      },
      read: function () { return this.ta && this.ta.__cm ? this.ta.__cm.getValue() : (this.ta ? this.ta.value : ""); },
      onText: function () { this.dirty = true; this.representable = this.parse(this.read()) !== null; },
      toBuilder: function () {
        var meta = this.parse(this.read());
        if (meta === null) return;
        this.version = meta.version; this.docId = meta.docId;
        this.bareStmt = meta.bareStmt; this.stmts = meta.stmts;
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
      addStatement: function () { this.stmts.push(newStatement()); this.sync(); },
      removeStatement: function (i) {
        this.stmts.splice(i, 1);
        if (!this.stmts.length) this.stmts.push(newStatement());
        this.sync();
      },
      /* Chip lists: commit the draft on Enter/comma/blur, never lose it. */
      commitDraft: function (st, which) {
        var field = which + "Draft", list = which === "action" ? st.actions : st.resources;
        var v = (st[field] || "").trim().replace(/,$/, "");
        if (v) { list.push(v); st[field] = ""; this.sync(); }
      },
      dropChip: function (list, i) { list.splice(i, 1); this.sync(); },
      addCond: function (st) { st.conds.push({ mod: "", op: "StringEquals", ifExists: false, key: "", vals: "" }); },
      addPrincipalRow: function (st) { st.prows.push({ type: "AWS", vals: "" }); },
    };
  };
})();
