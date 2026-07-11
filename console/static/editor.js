// editor.js — upgrades every textarea[data-editor] into a CodeMirror editor
// with JSON/YAML highlighting. Mode auto-detects from content ('{' or '[' →
// JSON, otherwise YAML) unless data-editor names one explicitly. Values sync
// back to the textarea before any htmx request or native form submit.
(function () {
  "use strict";

  function detectMode(text, hint) {
    if (hint === "json" || hint === "yaml") return hint === "json" ? { name: "javascript", json: true } : "yaml";
    var t = (text || "").trimStart();
    if (t.startsWith("{") || t.startsWith("[")) return { name: "javascript", json: true };
    return "yaml";
  }

  function upgrade(ta) {
    if (ta.__cm || typeof CodeMirror === "undefined") return;
    var hint = ta.getAttribute("data-editor");
    var cm = CodeMirror.fromTextArea(ta, {
      mode: detectMode(ta.value, hint),
      lineNumbers: true,
      matchBrackets: true,
      autoCloseBrackets: true,
      viewportMargin: Infinity,
      tabSize: 2,
      indentWithTabs: false,
      placeholder: ta.placeholder || "",
    });
    ta.__cm = cm;
    cm.on("change", function () {
      cm.save();
      if (hint !== "json" && hint !== "yaml") {
        var m = detectMode(cm.getValue(), null);
        var cur = cm.getOption("mode");
        var curIsJSON = typeof cur === "object" && cur.json;
        var nextIsJSON = typeof m === "object" && m.json;
        if (curIsJSON !== nextIsJSON) cm.setOption("mode", m);
      }
      // Re-emit as a bubbling input event so live listeners (debounced htmx
      // triggers, dirty flags) hear edits made inside the editor.
      ta.dispatchEvent(new Event("input", { bubbles: true }));
    });
    // Grow with content inside the min/max the CSS sets.
    cm.setSize("100%", null);
  }

  function upgradeAll(root) {
    (root || document).querySelectorAll("textarea[data-editor]").forEach(upgrade);
  }

  // Sync every editor back to its textarea before htmx serializes the form.
  document.addEventListener("htmx:configRequest", function () {
    document.querySelectorAll("textarea[data-editor]").forEach(function (ta) {
      if (ta.__cm) ta.__cm.save();
    });
  });
  document.addEventListener("submit", function () {
    document.querySelectorAll("textarea[data-editor]").forEach(function (ta) {
      if (ta.__cm) ta.__cm.save();
    });
  }, true);

  document.addEventListener("DOMContentLoaded", function () { upgradeAll(); });
  document.addEventListener("htmx:afterSwap", function (e) { upgradeAll(e.detail.target); });

  // Programmatic access (e.g. "Edit item" prefills the put-item editor).
  window.dozeEditor = {
    set: function (ta, value) {
      if (typeof ta === "string") ta = document.querySelector(ta);
      if (!ta) return;
      if (ta.__cm) { ta.__cm.setValue(value); ta.__cm.save(); ta.__cm.refresh(); }
      else ta.value = value;
    },
    refresh: function (root) {
      (root || document).querySelectorAll("textarea[data-editor]").forEach(function (ta) {
        if (ta.__cm) ta.__cm.refresh();
      });
    },
    upgradeAll: upgradeAll,
  };
})();
