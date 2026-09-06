/* Pan and zoom for the Step Functions graph, and the click that ties a node
 * to its history rows.
 *
 * The SVG is rendered at its natural size and moved with a CSS transform on
 * the element — translate then scale, origin top-left — because that keeps
 * the mapping from a pointer to a graph coordinate a single line. The view
 * is remembered per SVG id: the execution page redraws the graph every poll
 * while it is RUNNING, and a redraw that snapped back to fit-to-width would
 * make the picture impossible to look at.
 */
(function () {
  "use strict";

  var views = {}; // svg id → {s, tx, ty, h}; survives the live region's swaps
  var MIN = 0.2, MAX = 3;

  function apply(svg, v) {
    svg.style.transform = "translate(" + v.tx + "px," + v.ty + "px) scale(" + v.s + ")";
  }

  // Fit to width, capped at 1:1 — a three-state machine should not be blown
  // up to fill a 900px pane. The wrap's height follows the graph when the
  // graph is short, so a small machine does not sit in a large empty field.
  function fit(wrap, svg) {
    var w = parseFloat(svg.getAttribute("width")) || 1;
    var h = parseFloat(svg.getAttribute("height")) || 1;
    var s = Math.min(1, (wrap.clientWidth - 24) / w);
    return { s: s, tx: Math.max(12, (wrap.clientWidth - w * s) / 2), ty: 12, h: Math.max(160, Math.min(480, h * s + 24)) };
  }

  function zoomAt(svg, v, factor, mx, my) {
    var s = Math.min(MAX, Math.max(MIN, v.s * factor));
    // Keep the graph point under the pointer where it is.
    var gx = (mx - v.tx) / v.s, gy = (my - v.ty) / v.s;
    v.s = s; v.tx = mx - gx * s; v.ty = my - gy * s;
    apply(svg, v);
  }

  function setup(wrap) {
    var svg = wrap.querySelector("svg.graph");
    if (!svg || wrap.__graph) return;
    wrap.__graph = true;
    var v = views[svg.id] || fit(wrap, svg);
    views[svg.id] = v;
    wrap.style.height = v.h + "px";
    apply(svg, v);

    var drag = null;
    wrap.addEventListener("pointerdown", function (e) {
      if (e.button !== 0 || e.target.closest(".graph-tools")) return;
      // The bottom-right corner is the CSS resize handle, not a pan.
      var r = wrap.getBoundingClientRect();
      if (r.right - e.clientX < 18 && r.bottom - e.clientY < 18) return;
      drag = { x: e.clientX, y: e.clientY, tx: v.tx, ty: v.ty, moved: false };
      wrap.setPointerCapture(e.pointerId);
      wrap.classList.add("dragging");
    });
    wrap.addEventListener("pointermove", function (e) {
      if (!drag) return;
      var dx = e.clientX - drag.x, dy = e.clientY - drag.y;
      if (Math.abs(dx) + Math.abs(dy) > 3) drag.moved = true;
      v.tx = drag.tx + dx; v.ty = drag.ty + dy;
      apply(svg, v);
    });
    function endDrag() {
      v.h = wrap.clientHeight; // a resize by the corner handle survives the next swap
      if (!drag) return;
      var moved = drag.moved;
      drag = null;
      wrap.classList.remove("dragging");
      if (moved) wrap.__suppressClick = true; // a pan is not a click
    }
    wrap.addEventListener("pointerup", endDrag);
    wrap.addEventListener("pointercancel", endDrag);
    wrap.addEventListener("wheel", function (e) {
      e.preventDefault();
      var r = wrap.getBoundingClientRect();
      zoomAt(svg, v, e.deltaY > 0 ? 0.9 : 1.1, e.clientX - r.left, e.clientY - r.top);
    }, { passive: false });

    wrap.addEventListener("click", function (e) {
      if (wrap.__suppressClick) { wrap.__suppressClick = false; return; }
      var act = e.target.closest("[data-graph-act]");
      if (act) {
        var a = act.getAttribute("data-graph-act");
        if (a === "fit") { var f = fit(wrap, svg); v.s = f.s; v.tx = f.tx; v.ty = f.ty; apply(svg, v); }
        else zoomAt(svg, v, a === "in" ? 1.25 : 0.8, wrap.clientWidth / 2, wrap.clientHeight / 2);
        return;
      }
      var node = e.target.closest(".gn[data-state]");
      if (node) select(node.getAttribute("data-state"));
    });
    wrap.addEventListener("keydown", function (e) {
      var node = e.target.closest && e.target.closest(".gn[data-state]");
      if (node && (e.key === "Enter" || e.key === " ")) { e.preventDefault(); select(node.getAttribute("data-state")); }
    });
  }

  // Selecting a node highlights every history row for that state and
  // scrolls the first into view. On the machine page there are no rows, and
  // the node simply takes the selection ring. The selection is kept here,
  // not in the DOM, so the next poll's redraw paints it again.
  var selected = null;
  function select(name) {
    selected = selected === name ? null : name;
    paint();
    if (selected === null) return;
    var row = document.querySelector('tr[data-state="' + CSS.escape(name) + '"]');
    if (row) row.scrollIntoView({ block: "center", behavior: "smooth" });
  }
  function paint() {
    document.querySelectorAll(".gn[data-state]").forEach(function (n) {
      n.classList.toggle("gn-sel", n.getAttribute("data-state") === selected);
    });
    document.querySelectorAll("tr[data-state]").forEach(function (tr) {
      tr.classList.toggle("hl", selected !== null && tr.getAttribute("data-state") === selected);
    });
  }

  function setupAll() {
    document.querySelectorAll("[data-graph]").forEach(setup);
    if (selected !== null) paint();
  }
  document.addEventListener("DOMContentLoaded", setupAll);
  // A poll replaces the wrap outright (the live region swaps outerHTML), so
  // the guard on the old element goes with it and setup runs on the new one.
  document.addEventListener("htmx:afterSwap", setupAll);
})();
