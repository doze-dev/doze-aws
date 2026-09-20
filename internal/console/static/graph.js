/* Pan and zoom for the Step Functions graph, and the click that ties a node
 * to its history rows.
 *
 * The interaction is d3-zoom (static/d3-zoom.min.js): wheel and trackpad
 * zoom about the pointer, drag to pan, pinch on touch, double-click to zoom
 * in, and animated transitions for fit and focus. The SVG fills its pane
 * and the <g class="gv"> inside it carries the transform, so the mapping
 * from pointer to graph coordinate is d3's, not ours.
 *
 * The view is remembered per SVG id: the execution page redraws the graph
 * every poll while it is RUNNING, and a redraw that snapped back to
 * fit-to-width would make the picture impossible to look at. A redraw
 * re-applies the remembered transform without animation.
 */
(function () {
  "use strict";

  var views = {}; // svg id → {t: zoomTransform, h: pane height}
  var MIN = 0.15, MAX = 4, PAD = 16;

  function size(svg) {
    return { w: parseFloat(svg.getAttribute("data-w")) || 1, h: parseFloat(svg.getAttribute("data-h")) || 1 };
  }

  // Fit the whole graph in the pane, capped at 1:1 — a three-state machine
  // should not be blown up to fill a 900px pane. The pane's height follows
  // the graph when the graph is short, so a small machine does not sit in a
  // large empty field.
  function fitTransform(wrap, svg) {
    var g = size(svg);
    var pw = wrap.clientWidth, ph = wrap.clientHeight;
    var s = Math.min(1, (pw - 2 * PAD) / g.w, (ph - 2 * PAD) / g.h);
    return d3.zoomIdentity.translate(Math.max(PAD, (pw - g.w * s) / 2), Math.max(PAD, (ph - g.h * s) / 2)).scale(s);
  }

  function paneHeight(wrap, svg) {
    var g = size(svg);
    var s = Math.min(1, (wrap.clientWidth - 2 * PAD) / g.w);
    return Math.max(160, Math.min(480, g.h * s + 2 * PAD));
  }

  // Centre a node in the pane at a readable scale, animated.
  function focusOn(wrap, sel, zoom, node) {
    var m = /translate\(([-\d.]+)[ ,]([-\d.]+)\)/.exec(node.getAttribute("transform") || "");
    if (!m) return;
    var r = node.querySelector(".gn-r");
    var w = parseFloat(r && r.getAttribute("width")) || 120, h = parseFloat(r && r.getAttribute("height")) || 44;
    var cx = parseFloat(m[1]) + w / 2, cy = parseFloat(m[2]) + h / 2;
    var s = Math.max(1, Math.min(MAX, sel.property("__zoom").k));
    var t = d3.zoomIdentity.translate(wrap.clientWidth / 2 - cx * s, wrap.clientHeight / 2 - cy * s).scale(s);
    sel.transition().duration(450).call(zoom.transform, t);
  }

  function setup(wrap) {
    var svg = wrap.querySelector("svg.graph");
    if (!svg || wrap.__graph) return;
    wrap.__graph = true;
    var view = views[svg.id];
    wrap.style.height = (view ? view.h : paneHeight(wrap, svg)) + "px";

    var sel = d3.select(svg), inner = sel.select("g.gv");
    var zoom = d3.zoom()
      .scaleExtent([MIN, MAX])
      // The toolbar is not a gesture, and neither is the CSS resize handle
      // in the bottom-right corner of the pane.
      .filter(function (e) {
        if (e.target.closest && e.target.closest(".graph-tools")) return false;
        if (e.type === "mousedown" || e.type === "pointerdown") {
          var b = wrap.getBoundingClientRect();
          if (b.right - e.clientX < 18 && b.bottom - e.clientY < 18) return false;
          if (e.button !== 0) return false;
        }
        return !e.ctrlKey || e.type === "wheel"; // ctrl+wheel is a pinch on trackpads
      })
      .on("start", function () { wrap.classList.add("dragging"); })
      .on("zoom", function (e) {
        inner.attr("transform", e.transform);
        views[svg.id] = { t: e.transform, h: wrap.clientHeight };
      })
      .on("end", function () { wrap.classList.remove("dragging"); views[svg.id].h = wrap.clientHeight; });
    sel.call(zoom).on("dblclick.zoom", function (e) {
      // Double-click zooms in about the pointer, animated; d3's default
      // does the same but ignores the scale cap on the way.
      var b = svg.getBoundingClientRect(), px = e.clientX - b.left, py = e.clientY - b.top;
      var p = d3.zoomTransform(svg).invert([px, py]);
      var k = Math.min(MAX, d3.zoomTransform(svg).k * 1.6);
      sel.transition().duration(300).call(zoom.transform, d3.zoomIdentity.translate(px - p[0] * k, py - p[1] * k).scale(k));
    });
    // A remembered view is restored as it was; a first view is fitted.
    sel.call(zoom.transform, view ? view.t : fitTransform(wrap, svg));

    wrap.addEventListener("click", function (e) {
      var act = e.target.closest("[data-graph-act]");
      if (act) {
        switch (act.getAttribute("data-graph-act")) {
          case "fit": sel.transition().duration(350).call(zoom.transform, fitTransform(wrap, svg)); break;
          case "in": sel.transition().duration(200).call(zoom.scaleBy, 1.3); break;
          case "out": sel.transition().duration(200).call(zoom.scaleBy, 1 / 1.3); break;
          case "focus":
            var n = svg.querySelector(".gn-running[data-state]") || svg.querySelector(".gn-failed[data-state]");
            if (n) { focusOn(wrap, sel, zoom, n); select(n.getAttribute("data-state"), true); }
            break;
        }
        return;
      }
      // d3-zoom swallows the click that ends a drag, so a plain click here
      // is a real click on a node.
      var node = e.target.closest(".gn[data-state]");
      if (node) select(node.getAttribute("data-state"));
    });
    wrap.addEventListener("keydown", function (e) {
      var node = e.target.closest && e.target.closest(".gn[data-state]");
      if (node && (e.key === "Enter" || e.key === " ")) { e.preventDefault(); select(node.getAttribute("data-state")); }
    });
    // The pane can be resized by its corner handle; the graph re-fits only
    // if it was never touched, otherwise the view is the user's.
    if (window.ResizeObserver) {
      new ResizeObserver(function () { if (views[svg.id]) views[svg.id].h = wrap.clientHeight; }).observe(wrap);
    }
  }

  // Selecting a node highlights every history row for that state and
  // scrolls the first into view. On the machine page there are no rows, and
  // the node simply takes the selection ring. The selection is kept here,
  // not in the DOM, so the next poll's redraw paints it again.
  var selected = null;
  function select(name, keep) {
    selected = keep ? name : (selected === name ? null : name);
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
    if (!window.d3) return;
    document.querySelectorAll("[data-graph]").forEach(setup);
    if (selected !== null) paint();
  }
  document.addEventListener("DOMContentLoaded", setupAll);
  // A poll replaces the wrap outright (the live region swaps outerHTML), so
  // the guard on the old element goes with it and setup runs on the new one.
  document.addEventListener("htmx:after:swap", setupAll);
})();
