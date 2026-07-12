// shell.js — workbench chrome: theme, toasts, copy, styled confirms, command
// palette, keyboard navigation, and live-region polling helpers. Everything
// here lives OUTSIDE #workspace so it survives every content swap.
(function () {
  "use strict";
  var PREFIX = (document.querySelector('link[rel="icon"]') || {}).href
    ? new URL(document.querySelector('link[rel="icon"]').href).pathname.replace(/\/static\/favicon\.svg$/, "")
    : "/_console";

  // ---------- Alpine glue ----------
  document.addEventListener("htmx:afterSwap", function () {
    // e.detail.target is the OLD detached node for outerHTML swaps; upgrade
    // document-wide instead (both calls are idempotent). Alpine also inits new
    // nodes via its own MutationObserver, but initTree on the live tree is safe.
    if (window.Alpine) window.Alpine.initTree(document.body);
    if (window.dozeEditor) window.dozeEditor.upgradeAll();
    resetListCursor();
  });

  // ---------- theme ----------
  var toggle = document.getElementById("theme-toggle");
  function isDark() {
    return (document.documentElement.getAttribute("data-theme") ||
      (matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light")) === "dark";
  }
  if (toggle) toggle.addEventListener("click", function () {
    var next = isDark() ? "light" : "dark";
    document.documentElement.setAttribute("data-theme", next);
    try { localStorage.setItem("theme", next); } catch (e) {}
  });

  // ---------- collapsible rail ----------
  function railSlim() { return document.documentElement.getAttribute("data-rail") === "slim"; }
  function setRail(slim) {
    if (slim) document.documentElement.setAttribute("data-rail", "slim");
    else document.documentElement.removeAttribute("data-rail");
    try { localStorage.setItem("rail", slim ? "slim" : "wide"); } catch (e) {}
  }
  var railToggle = document.getElementById("rail-toggle");
  if (railToggle) railToggle.addEventListener("click", function () { setRail(!railSlim()); });
  // Flyout labels in slim mode: position fixed on hover so the rail's own
  // scroll/overflow can never clip them.
  document.addEventListener("mouseover", function (e) {
    if (!railSlim()) return;
    var ri = e.target.closest(".rail .ri");
    if (!ri) return;
    var fly = ri.querySelector(".ri-fly");
    if (!fly) return;
    var r = ri.getBoundingClientRect();
    fly.style.left = (r.right + 8) + "px";
    fly.style.top = (r.top + r.height / 2) + "px";
  });
  // Live per-service counts, refreshed gently. textContent writes are no-ops
  // visually when the number hasn't changed, so there's nothing to de-jitter.
  function refreshCounts() {
    if (document.hidden) return;
    fetch(PREFIX + "/api/counts").then(function (r) { return r.json(); }).then(function (counts) {
      document.querySelectorAll(".rail [data-ct]").forEach(function (el) {
        var n = counts[el.getAttribute("data-ct")];
        el.textContent = n == null ? "" : String(n);
      });
    }).catch(function () {});
  }
  refreshCounts();
  setInterval(refreshCounts, 5000);

  // ---------- toasts ----------
  var seq = 0;
  function toast(msg, kind) {
    var box = document.getElementById("toasts");
    if (!box) return;
    var el = document.createElement("div");
    el.className = "toast anim" + (kind === "err" ? " err" : "");
    el.innerHTML = '<span class="t-ic">' + (kind === "err" ? "⚠" : "✓") + '</span><span></span><span class="tclose">✕</span>';
    el.children[1].textContent = msg;
    el.querySelector(".tclose").onclick = function () { el.remove(); };
    box.appendChild(el);
    var id = ++seq;
    setTimeout(function () { el.remove(); }, kind === "err" ? 6000 : 3200);
  }
  window.addEventListener("toast", function (e) { toast(e.detail.value !== undefined ? e.detail.value : e.detail, "ok"); });
  window.addEventListener("toast-error", function (e) { toast(e.detail.value !== undefined ? e.detail.value : e.detail, "err"); });
  document.addEventListener("htmx:responseError", function (e) {
    var raw = (e.detail.xhr.responseText || "Request failed");
    // The server HTML-escapes error bodies; decode entities first so the message
    // regex matches and the toast shows real quotes/brackets, not "&#34;".
    var ta = document.createElement("textarea");
    ta.innerHTML = raw;
    raw = ta.value;
    var m = raw.match(/<Message>([^<]+)|"message"\s*:\s*"([^"]+)"/i);
    var msg = (m && (m[1] || m[2])) || raw.replace(/<[^>]*>/g, " ").replace(/\s+/g, " ").trim();
    if (msg.length > 140) msg = msg.slice(0, 140) + "…";
    toast(msg, "err");
  });

  // ---------- copy ----------
  document.addEventListener("click", function (e) {
    var el = e.target.closest("[data-copy]");
    if (!el) return;
    navigator.clipboard.writeText(el.getAttribute("data-copy")).then(function () { toast("Copied"); });
  });

  // ---------- styled confirm (intercepts hx-confirm) ----------
  var confirmBox = document.getElementById("confirm");
  document.addEventListener("htmx:confirm", function (e) {
    var q = e.detail.question;
    if (!q || !confirmBox) return;
    e.preventDefault();
    document.getElementById("confirm-msg").textContent = q;
    confirmBox.hidden = false;
    var yes = document.getElementById("confirm-yes"), no = document.getElementById("confirm-no");
    function close() { confirmBox.hidden = true; yes.onclick = no.onclick = confirmBox.onclick = null; document.removeEventListener("keydown", onKey); }
    function onKey(ev) { if (ev.key === "Escape") close(); }
    yes.onclick = function () { close(); e.detail.issueRequest(true); };
    no.onclick = close;
    confirmBox.onclick = function (ev) { if (ev.target === confirmBox) close(); };
    document.addEventListener("keydown", onKey);
    yes.focus();
  });

  // ---------- command palette ----------
  var pal = document.getElementById("palette"), palQ = document.getElementById("pal-q"), palList = document.getElementById("pal-list");
  var palItems = [], palSel = 0;
  var NAV = [["s3","S3","/s3"],["ddb","DynamoDB","/ddb"],["lambda","Lambda","/lambda"],["sqs","SQS","/sqs"],
    ["sns","SNS","/sns"],["eb","EventBridge","/eb"],["kms","KMS","/kms"],["sm","Secrets Manager","/sm"],["ssm","Parameter Store","/ssm"]];
  var ACTS = [["s3","Create bucket","/s3/create"],["sqs","Create queue","/sqs/create"],["ddb","Create table","/ddb/create"],
    ["sns","Create topic","/sns/create"],["eb","Create event bus","/eb/create-bus"],["kms","Create key","/kms/create"],
    ["ssm","Create parameter","/ssm/create"],["sm","Store secret","/sm/create"]];
  var KIND = { s3:"bucket", sqs:"queue", ddb:"table", sns:"topic", eb:"bus / rule", lambda:"function", kms:"key", ssm:"parameter", sm:"secret" };

  // ---------- recently visited resources ----------
  // The palette pins your last few resource pages on top: the queue you just
  // left costs ⌘K ⏎ instead of retyping its name.
  var SVCSET = { s3:1, ddb:1, lambda:1, sqs:1, sns:1, eb:1, kms:1, ssm:1, sm:1 };
  function trackVisit() {
    try {
      var path = location.pathname;
      if (path.indexOf(PREFIX + "/") !== 0) return;
      var rest = path.slice(PREFIX.length + 1).split("/");
      var svc = rest[0];
      if (!SVCSET[svc]) return;
      var name = "", u = path;
      var qname = new URLSearchParams(location.search).get("name");
      if ((svc === "ssm" || svc === "sm") && qname) {
        name = qname;
        u = path + "?name=" + encodeURIComponent(qname);
      } else if (rest.length >= 2 && rest[1] && rest[1].indexOf("create") < 0) {
        name = decodeURIComponent(rest[1]);
        if (svc === "eb" && rest[2] === "rule" && rest[3]) name += " › " + decodeURIComponent(rest[3]);
      }
      if (!name) return;
      var rec = JSON.parse(localStorage.getItem("recents") || "[]").filter(function (x) { return x.u !== u; });
      rec.unshift({ s: svc, n: name, u: u });
      localStorage.setItem("recents", JSON.stringify(rec.slice(0, 6)));
    } catch (e) {}
  }
  document.addEventListener("DOMContentLoaded", trackVisit);
  document.addEventListener("htmx:pushedIntoHistory", trackVisit);

  // ---------- rail active state ----------
  // The rail lives outside #workspace, so boosted navigation swaps the page
  // without repainting the server-rendered `.on` class. Recompute the active
  // item from the URL after every navigation (and on back/forward).
  function syncRail() {
    var here = location.pathname;
    document.querySelectorAll(".rail .ri").forEach(function (a) {
      var ip = new URL(a.href, location.origin).pathname;
      var on = ip.charAt(ip.length - 1) === "/"
        ? (here === ip || here === ip.slice(0, -1))       // Flows (root)
        : (here === ip || here.indexOf(ip + "/") === 0);  // a service section
      a.classList.toggle("on", on);
    });
  }
  document.addEventListener("DOMContentLoaded", syncRail);
  document.addEventListener("htmx:pushedIntoHistory", syncRail);
  window.addEventListener("popstate", syncRail);

  function openPalette() {
    if (!pal) return;
    pal.hidden = false; palQ.value = ""; palSel = 0;
    var here = location.pathname + location.search;
    var recents = [];
    try {
      recents = JSON.parse(localStorage.getItem("recents") || "[]")
        .filter(function (x) { return x.u !== here; }).slice(0, 5)
        .map(function (x) { return { s: x.s, n: x.n, u: x.u, k: "recent" }; });
    } catch (e) {}
    var fixed = NAV.map(function (n) { return { s:n[0], n:n[1], u:PREFIX+n[2], k:"service" }; })
      .concat([{ s:"", n:"Flows", u:PREFIX+"/", k:"surface" }, { s:"", n:"Traffic", u:PREFIX+"/traffic", k:"surface" }])
      .concat(ACTS.map(function (a) { return { s:a[0], n:a[1], u:PREFIX+a[2], k:"action" }; }));
    palItems = recents.concat(fixed);
    renderPal();
    palQ.focus();
    fetch(PREFIX + "/api/resources").then(function (r) { return r.json(); }).then(function (rs) {
      var seen = {};
      recents.forEach(function (x) { seen[x.u] = 1; });
      var fresh = (rs || []).filter(function (r) { return !seen[r.u]; })
        .map(function (r) { r.k = KIND[r.s] || r.s; return r; });
      palItems = recents.concat(fresh).concat(fixed);
      renderPal();
    });
  }
  function palFiltered() {
    var q = palQ.value.toLowerCase().trim();
    if (!q) return palItems.slice(0, 10);
    return palItems.filter(function (it) { return (it.n + " " + it.k).toLowerCase().indexOf(q) >= 0; }).slice(0, 10);
  }
  function renderPal() {
    var items = palFiltered();
    if (palSel >= items.length) palSel = Math.max(0, items.length - 1);
    palList.innerHTML = "";
    if (!items.length) { palList.innerHTML = '<div class="pal-empty">Nothing matches</div>'; return; }
    items.forEach(function (it, i) {
      var a = document.createElement("a");
      a.className = "pal-item" + (i === palSel ? " sel" : "");
      a.href = it.u;
      a.innerHTML = (it.s ? '<img class="aws-ic sm" src="' + PREFIX + '/static/aws/' + it.s + '.svg">' : '<span class="pal-dot"></span>') +
        '<span class="pal-name"></span><span class="pal-kind"></span>';
      a.querySelector(".pal-name").textContent = it.n;
      a.querySelector(".pal-kind").textContent = it.k;
      a.onmouseenter = function () { palSel = i; renderPal(); };
      palList.appendChild(a);
    });
  }
  function closePalette() { if (pal) pal.hidden = true; }
  var opener = document.getElementById("palette-open");
  if (opener) opener.addEventListener("click", openPalette);
  if (palQ) palQ.addEventListener("input", function () { palSel = 0; renderPal(); });
  if (pal) pal.addEventListener("click", function (e) { if (e.target === pal) closePalette(); });

  // ---------- keyboard ----------
  var cursor = -1;
  function listRows() { return Array.prototype.slice.call(document.querySelectorAll(".listpane .li[href]")); }
  function resetListCursor() { cursor = -1; }
  function moveCursor(d) {
    var rows = listRows();
    if (!rows.length) return;
    cursor = Math.min(rows.length - 1, Math.max(0, cursor + d));
    rows.forEach(function (r, i) { r.classList.toggle("cursor", i === cursor); });
    rows[cursor].scrollIntoView({ block: "nearest" });
  }
  document.addEventListener("keydown", function (e) {
    var inPal = !pal.hidden;
    if (inPal) {
      var items = palFiltered();
      if (e.key === "Escape") { closePalette(); e.preventDefault(); }
      else if (e.key === "ArrowDown") { palSel = Math.min(palSel + 1, items.length - 1); renderPal(); e.preventDefault(); }
      else if (e.key === "ArrowUp") { palSel = Math.max(palSel - 1, 0); renderPal(); e.preventDefault(); }
      else if (e.key === "Enter") { var it = items[palSel]; if (it) location.href = it.u; e.preventDefault(); }
      return;
    }
    if ((e.metaKey || e.ctrlKey) && e.key === "k") { e.preventDefault(); openPalette(); return; }
    // ⌘⏎ submits the composer you're typing in — send, publish, invoke, put.
    // Works from plain textareas and from inside CodeMirror editors alike.
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      var form = document.activeElement.closest ? document.activeElement.closest("form") : null;
      if (form && form.querySelector("textarea, .CodeMirror")) { e.preventDefault(); form.requestSubmit(); return; }
    }
    var tag = document.activeElement.tagName;
    if (/(INPUT|TEXTAREA|SELECT)/.test(tag) || document.activeElement.closest(".CodeMirror")) return;
    if (e.key === "/") {
      var f = document.querySelector(".listpane .filter input");
      if (f) { f.focus(); e.preventDefault(); } else openPalette();
    } else if (e.key === "j") moveCursor(1);
    else if (e.key === "k") moveCursor(-1);
    else if (e.key === "Enter" && cursor >= 0) { var r = listRows()[cursor]; if (r) r.click(); }
    else if (e.key === "c") { var nb = document.querySelector(".listpane .new-link"); if (nb) nb.click(); }
    else if (e.key === "[") { setRail(!railSlim()); }
    else if (e.key === "Escape") { var back = document.querySelector("[data-esc-back]"); if (back) history.back(); }
  });

  // ---------- live polling with change detection ----------
  // Elements with data-live="URL" poll every data-live-ms (default 3000);
  // the server echoes a content hash — when unchanged it replies 204 and
  // nothing moves. Changed content is morph-swapped so selection survives.
  var liveTimers = [];
  function setupLive() {
    liveTimers.forEach(clearInterval);
    liveTimers = [];
    document.querySelectorAll("[data-live]").forEach(function (el) {
      var id = el.id;
      var every = parseInt(el.getAttribute("data-live-ms") || "3000", 10);
      var t = setInterval(function () {
        // Re-query by id every tick: a plain outerHTML swap (composer send,
        // purge, redrive, delete) REPLACES the element, so a captured reference
        // would go stale and the poll would silently die. The morph poll itself
        // re-includes data-live, so a morphed element keeps the same id too.
        var cur = document.getElementById(id);
        if (document.hidden || !cur) return;
        var url = cur.getAttribute("data-live");
        if (!url) return;
        // Most live regions morph so selection/scroll survive; a small self-
        // contained element can opt into a plain outerHTML swap via
        // data-live-swap to avoid idiomorph nesting its replacement.
        var swap = cur.getAttribute("data-live-swap") || "morph:outerHTML";
        var hash = cur.getAttribute("data-hash") || "";
        htmx.ajax("GET", url + (url.indexOf("?") >= 0 ? "&" : "?") + "h=" + hash, {
          target: "#" + id, swap: swap,
        });
      }, every);
      liveTimers.push(t);
    });
  }
  document.addEventListener("DOMContentLoaded", setupLive);
  document.addEventListener("htmx:afterSwap", function (e) {
    // Re-arm after a navigation OR after a swap that lands (or contains) a live
    // region — including one whose target IS the data-live element itself, which
    // a descendant-only querySelector check would miss.
    var t = e.detail.target;
    if (t && (t.id === "workspace" ||
              (t.matches && t.matches("[data-live]")) ||
              (t.querySelector && t.querySelector("[data-live]")))) setupLive();
  });
  // After each poll, stamp the element's data-hash from the response header — a
  // morph swap doesn't reliably update the root's attributes, so without this the
  // poll would keep re-fetching the same change every tick.
  document.addEventListener("htmx:afterRequest", function (e) {
    var xhr = e.detail.xhr;
    if (!xhr) return;
    var h = xhr.getResponseHeader("HX-Live-Hash");
    if (!h) return;
    var path = (e.detail.requestConfig && e.detail.requestConfig.path) || "";
    document.querySelectorAll("[data-live]").forEach(function (el) {
      var base = el.getAttribute("data-live");
      if (base && path.indexOf(base) === 0) el.setAttribute("data-hash", h);
    });
  });

  // ---------- sleep countdowns ----------
  // A .rt-cd inside an element carrying data-sleep-left="<seconds>" counts down
  // every second. The server sends seconds-remaining (not an absolute time), and
  // each fresh element re-bases it against the browser's own clock — so a
  // browser/server clock skew can't drift the countdown. The 3s live poll swaps
  // in a fresh element (with a new _deadline) only on a reset; the going-cold
  // flip is server-authoritative, arriving as a swapped-in grey badge.
  function fmtLeft(s) {
    return s >= 60 ? Math.floor(s / 60) + "m " + String(s % 60).padStart(2, "0") + "s" : s + "s";
  }
  setInterval(function () {
    document.querySelectorAll(".rt-cd").forEach(function (cd) {
      var host = cd.closest("[data-sleep-left]");
      if (!host) return;
      if (host._deadline == null) {
        host._deadline = Date.now() + parseInt(host.getAttribute("data-sleep-left") || "0", 10) * 1000;
      }
      var left = Math.round((host._deadline - Date.now()) / 1000);
      cd.textContent = left > 0 ? fmtLeft(left) : "any moment";
    });
  }, 1000);

  window.dozeShell = { toast: toast, openPalette: openPalette };
})();
