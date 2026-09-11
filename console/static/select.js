// select.js — every <select> becomes a listbox the console can style.
//
// # Why enhance rather than replace
//
// There are 111 selects across twenty-one templates. 102 of them post inside a
// form, seventeen are bound to Alpine with x-model, several carry `required`,
// and every one of them is serialized by htmx when its form is submitted.
// Replacing the markup at 111 call sites would put all of that at risk for a
// visual change.
//
// So the native select stays in the DOM and stays the source of truth. It is
// moved out of sight, not removed: the form posts it, `required` still blocks
// a submit, htmx still serializes it, Alpine's x-model still reads and writes
// it, and a browser with no JavaScript still gets a working control. The
// listbox on top is a projection that writes back through it and fires the
// `change` event everything else already listens for.
//
// This is the same contract editor.js has with textarea[data-editor], and the
// same reason: the thing that was there keeps working, and the enhancement is
// allowed to fail without taking the form with it.
//
// # Opting out
//
// data-native on the select. Nothing uses it yet; it is there for the case
// where a browser control is genuinely wanted.

(function () {
  "use strict";

  // Above this many options the popover grows a filter box, which is the only
  // thing that makes a long list usable by keyboard. Below it a filter is
  // clutter, and type-ahead covers it.
  var FILTER_FROM = 8;

  function optionsOf(sel) {
    return Array.prototype.map.call(sel.options, function (o, i) {
      return { i: i, value: o.value, label: o.textContent.trim(), disabled: o.disabled };
    });
  }

  function upgrade(sel) {
    if (sel.__ds || sel.multiple || sel.hasAttribute("data-native")) return;
    if (sel.closest(".CodeMirror")) return;

    var wrap = document.createElement("div");
    wrap.className = "ds" + (sel.className ? " ds-" + (sel.classList.contains("chip") ? "chip" : "input") : "");
    sel.parentNode.insertBefore(wrap, sel);
    wrap.appendChild(sel);
    sel.classList.add("ds-native");

    // The wrapper is the layout box now, so anything the select was sized with
    // has to move onto it or the control loses its width. Inline styles are
    // copied here; the three CSS rules that sized a select as a flex child
    // (.sub-add, .fv-row, .sfn-route) were repointed at .ds in app.css.
    ["width", "minWidth", "maxWidth", "flex", "flexBasis"].forEach(function (p) {
      if (sel.style[p]) { wrap.style[p] = sel.style[p]; sel.style[p] = ""; }
    });
    var inlineHeight = sel.style.height, inlineFont = sel.style.fontSize;

    var trigger = document.createElement("button");
    trigger.type = "button";
    trigger.className = "ds-trigger" + (sel.classList.contains("chip") ? " chip" : " input");
    trigger.setAttribute("aria-haspopup", "listbox");
    trigger.setAttribute("aria-expanded", "false");
    // The native select carries the accessible name — a label[for], an
    // aria-label or a title — so the trigger borrows it rather than inventing
    // a second one that could disagree.
    var labelledBy = sel.getAttribute("aria-labelledby");
    var ariaLabel = sel.getAttribute("aria-label") || sel.getAttribute("title");
    if (labelledBy) trigger.setAttribute("aria-labelledby", labelledBy);
    else if (ariaLabel) trigger.setAttribute("aria-label", ariaLabel);
    else if (sel.id && document.querySelector('label[for="' + sel.id + '"]')) {
      var lb = document.querySelector('label[for="' + sel.id + '"]');
      if (!lb.id) lb.id = "dslbl-" + Math.random().toString(36).slice(2, 8);
      trigger.setAttribute("aria-labelledby", lb.id);
    }
    if (sel.disabled) trigger.disabled = true;
    if (inlineHeight) trigger.style.height = inlineHeight;
    if (inlineFont) trigger.style.fontSize = inlineFont;

    var text = document.createElement("span");
    text.className = "ds-text";
    var caret = document.createElement("span");
    caret.className = "ds-caret";
    caret.setAttribute("aria-hidden", "true");
    caret.textContent = "⌄";
    trigger.appendChild(text);
    trigger.appendChild(caret);
    wrap.appendChild(trigger);

    var pop = document.createElement("div");
    pop.className = "ds-pop";
    pop.hidden = true;
    var filter = document.createElement("input");
    filter.className = "ds-filter";
    filter.type = "text";
    filter.placeholder = "Filter…";
    filter.setAttribute("aria-label", "Filter the options");
    var list = document.createElement("div");
    list.className = "ds-list";
    list.setAttribute("role", "listbox");
    pop.appendChild(filter);
    pop.appendChild(list);
    wrap.appendChild(pop);

    var state = { open: false, active: -1, items: [], typed: "", typedAt: 0 };
    sel.__ds = { sync: syncFromNative };

    function label() {
      var o = sel.options[sel.selectedIndex];
      return o ? o.textContent.trim() : "";
    }

    function syncFromNative() {
      var l = label();
      text.textContent = l;
      text.classList.toggle("ds-empty", l === "");
      if (!state.open) return;
      render(filter.value);
    }

    function render(q) {
      var needle = (q || "").toLowerCase();
      var opts = optionsOf(sel).filter(function (o) {
        return !needle || o.label.toLowerCase().indexOf(needle) >= 0;
      });
      state.items = opts;
      list.textContent = "";
      if (!opts.length) {
        var none = document.createElement("div");
        none.className = "ds-none";
        none.textContent = "Nothing matches";
        list.appendChild(none);
        state.active = -1;
        return;
      }
      opts.forEach(function (o, n) {
        var row = document.createElement("div");
        row.className = "ds-opt";
        row.setAttribute("role", "option");
        row.id = "dsopt-" + n;
        var chosen = o.i === sel.selectedIndex;
        row.setAttribute("aria-selected", String(chosen));
        if (o.disabled) row.setAttribute("aria-disabled", "true");
        var tick = document.createElement("span");
        tick.className = "ds-tick";
        tick.setAttribute("aria-hidden", "true");
        tick.textContent = chosen ? "✓" : "";
        var name = document.createElement("span");
        name.className = "ds-opt-t";
        name.textContent = o.label;
        row.appendChild(tick);
        row.appendChild(name);
        row.addEventListener("mousedown", function (e) {
          e.preventDefault(); // keep focus on the trigger
          if (!o.disabled) choose(o.i);
        });
        row.addEventListener("mousemove", function () { setActive(n); });
        list.appendChild(row);
      });
      var sel_n = opts.findIndex(function (o) { return o.i === sel.selectedIndex; });
      setActive(sel_n >= 0 ? sel_n : 0);
    }

    // Arrow movement wraps, which is what a listbox does and what makes the
    // last option reachable from the first without a long hold.
    function step(delta) {
      if (!state.items.length) return;
      var n = state.active;
      for (var tries = 0; tries < state.items.length; tries++) {
        n = (n + delta + state.items.length) % state.items.length;
        if (!state.items[n].disabled) break;
      }
      setActive(n);
    }

    function setActive(n) {
      state.active = n;
      var rows = list.querySelectorAll(".ds-opt");
      for (var i = 0; i < rows.length; i++) rows[i].classList.toggle("on", i === n);
      var row = rows[n];
      if (row) {
        list.setAttribute("aria-activedescendant", row.id);
        var top = row.offsetTop, bottom = top + row.offsetHeight;
        if (top < list.scrollTop) list.scrollTop = top;
        else if (bottom > list.scrollTop + list.clientHeight) list.scrollTop = bottom - list.clientHeight;
      }
    }

    function choose(index) {
      if (sel.selectedIndex !== index) {
        sel.selectedIndex = index;
        // The event everything downstream already listens for: Alpine's
        // x-model, any @change handler, and htmx triggers on the element.
        sel.dispatchEvent(new Event("input", { bubbles: true }));
        sel.dispatchEvent(new Event("change", { bubbles: true }));
      }
      close();
      syncFromNative();
    }

    function open() {
      if (state.open || sel.disabled) return;
      state.open = true;
      pop.hidden = false;
      trigger.setAttribute("aria-expanded", "true");
      var many = sel.options.length >= FILTER_FROM;
      filter.hidden = !many;
      filter.value = "";
      render("");
      // Open upward when there is more room above, so a control near the
      // bottom of a long page does not drop its list off the screen.
      var r = trigger.getBoundingClientRect();
      wrap.classList.toggle("ds-up", window.innerHeight - r.bottom < 240 && r.top > 240);
      if (many) filter.focus();
    }

    function close() {
      if (!state.open) return;
      state.open = false;
      pop.hidden = true;
      trigger.setAttribute("aria-expanded", "false");
      list.removeAttribute("aria-activedescendant");
    }

    trigger.addEventListener("click", function () { state.open ? close() : open(); });

    trigger.addEventListener("keydown", function (e) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp" || e.key === "Enter" || e.key === " ") {
        if (!state.open) { e.preventDefault(); open(); return; }
      }
      onKey(e);
    });
    filter.addEventListener("keydown", onKey);
    filter.addEventListener("input", function () { render(filter.value); });

    function onKey(e) {
      if (!state.open) return;
      switch (e.key) {
        case "ArrowDown": e.preventDefault(); step(1); break;
        case "ArrowUp": e.preventDefault(); step(-1); break;
        case "Home": e.preventDefault(); setActive(0); break;
        case "End": e.preventDefault(); setActive(state.items.length - 1); break;
        case "Enter":
          e.preventDefault();
          if (state.items[state.active]) choose(state.items[state.active].i);
          break;
        case "Escape":
          e.preventDefault();
          close();
          trigger.focus();
          break;
        case "Tab": close(); break;
        default:
          // Type-ahead, for the short lists that have no filter box.
          if (filter.hidden && e.key.length === 1) {
            var now = Date.now();
            state.typed = now - state.typedAt < 900 ? state.typed + e.key : e.key;
            state.typedAt = now;
            var hit = state.items.findIndex(function (o) {
              return o.label.toLowerCase().indexOf(state.typed.toLowerCase()) === 0;
            });
            if (hit >= 0) setActive(hit);
          }
      }
    }

    document.addEventListener("mousedown", function (e) {
      if (state.open && !wrap.contains(e.target)) close();
    });
    trigger.addEventListener("blur", function () {
      // Let a click inside the popover land first.
      setTimeout(function () { if (!wrap.contains(document.activeElement)) close(); }, 0);
    });

    // Alpine writes straight to the element when x-model changes, and that
    // fires no event — so the trigger has to notice on its own or it will show
    // a stale label.
    sel.addEventListener("change", syncFromNative);
    new MutationObserver(syncFromNative).observe(sel, {
      attributes: true, attributeFilter: ["value", "disabled"], childList: true, subtree: true,
    });
    sel.addEventListener("ds:sync", syncFromNative);

    syncFromNative();
    if (sel.disabled) trigger.disabled = true;
  }

  function upgradeAll(root) {
    (root || document).querySelectorAll("select:not(.ds-native)").forEach(upgrade);
  }

  // x-model assignments do not fire an event. A cheap pass on the frame after
  // any Alpine mutation keeps the label honest without polling forever.
  function resyncAll() {
    document.querySelectorAll("select.ds-native").forEach(function (s) {
      if (s.__ds) s.__ds.sync();
    });
  }

  document.addEventListener("DOMContentLoaded", function () { upgradeAll(); });
  // Same reasoning as editor.js: htmx reports the OLD node as the swap target
  // for an outerHTML swap, so upgrade document-wide. upgrade() skips anything
  // already done, so the pass is cheap.
  document.addEventListener("htmx:after:swap", function () { upgradeAll(); resyncAll(); });
  document.addEventListener("htmx:after:settle", resyncAll);
  document.addEventListener("alpine:initialized", function () { upgradeAll(); resyncAll(); });
  document.addEventListener("click", function () { setTimeout(resyncAll, 0); }, true);

  window.dozeSelect = { upgradeAll: upgradeAll, resync: resyncAll };
})();
