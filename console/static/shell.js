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

  // ---------- appearance ----------
  // Three states, not two. "system" is the absence of data-theme, which is what
  // lets the media query in app.css answer — so following the OS costs no JS at
  // all once the attribute is cleared, including on the pre-paint script.
  var seg = document.getElementById("appearance");
  function currentMode() {
    try { return localStorage.getItem("theme") || "system"; } catch (e) { return "system"; }
  }
  function applyMode(mode) {
    if (mode === "system") document.documentElement.removeAttribute("data-theme");
    else document.documentElement.setAttribute("data-theme", mode);
    try {
      if (mode === "system") localStorage.removeItem("theme");
      else localStorage.setItem("theme", mode);
    } catch (e) {}
    if (!seg) return;
    var buttons = seg.querySelectorAll("button");
    for (var i = 0; i < buttons.length; i++) {
      buttons[i].setAttribute("aria-pressed", String(buttons[i].dataset.mode === mode));
    }
  }
  if (seg) {
    seg.addEventListener("click", function (e) {
      var b = e.target.closest("button[data-mode]");
      if (b) applyMode(b.dataset.mode);
    });
    applyMode(currentMode());
  }

  // ---------- collapsible rail ----------
  function railSlim() { return document.documentElement.getAttribute("data-rail") === "slim"; }
  function setRail(slim) {
    // "wide" is written explicitly rather than clearing the attribute, because
    // below 1180px the stylesheet auto-slims the rail as a FLOOR — and someone
    // who expanded it at 1100px meant it, so their choice has to be able to win.
    if (slim) document.documentElement.setAttribute("data-rail", "slim");
    else document.documentElement.setAttribute("data-rail", "wide");
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
  // Live per-service counts. The poll's job is now much narrower than it was:
  // the rail is server-rendered and every navigation repaints it out of band
  // (see the ri template), so this exists ONLY to catch mutations the console
  // did not make — a queue filled by your app, a table an SDK wrote to. The
  // push path is the latency fix; this is the completeness fix.
  //
  // patchedAt is the one race worth guarding. A poll already in flight when a
  // mutation lands carries the count from BEFORE it, and would walk a freshly
  // patched badge backwards for one interval. An out-of-band patch is by
  // construction newer than any request that was already open, so a just-
  // patched badge is left alone rather than raced. Same number, same probe —
  // an overlap is a redundant identical write, not a conflict.
  var patchedAt = Object.create(null);
  document.body.addEventListener("htmx:oobAfterSwap", function (e) {
    var t = e.detail && e.detail.target;
    if (t && t.id) patchedAt[t.id] = Date.now();
  });
  function refreshCounts() {
    if (document.hidden) return;
    fetch(PREFIX + "/api/counts").then(function (r) { return r.json(); }).then(function (counts) {
      document.querySelectorAll(".rail [data-ct]").forEach(function (el) {
        if (Date.now() - (patchedAt[el.id] || 0) < 2500) return; // the patch is newer
        var n = counts[el.getAttribute("data-ct")];
        // A zero renders as nothing. Thirteen grey zeros on a fresh stack say
        // "empty" thirteen times; blank says "here is what you have".
        el.textContent = n ? String(n) : "";
      });
    }).catch(function () {});
  }
  setInterval(refreshCounts, 5000);
  // No refreshCounts() on load any more — the markup already carries the
  // counts, and calling it here only raced the server-rendered values with a
  // second fetch of the same numbers.
  //
  // The early return on document.hidden had no counterpart, so a tab left in
  // the background came back showing whatever it had when you switched away
  // and waited up to another five seconds to catch up.
  document.addEventListener("visibilitychange", function () {
    if (!document.hidden) refreshCounts();
  });

  // ---------- toasts ----------
  // The feedback sequence survives navigation. It seeds from sessionStorage
  // because a plain counter restarts at zero on every page load — so anything
  // holding a cursor across a navigation (the e2e fixture does) would wait for
  // seq 2 while the fresh page hands out seq 1 again, forever.
  var seq = 0;
  try { seq = Number(sessionStorage.getItem("dozeFeedbackSeq")) || 0; } catch (e) {}
  function nextSeq() {
    seq++;
    try { sessionStorage.setItem("dozeFeedbackSeq", String(seq)); } catch (e) {}
    return seq;
  }
  function toast(msg, kind) {
    var box = document.getElementById("toasts");
    if (!box) return;
    var el = document.createElement("div");
    el.className = "toast anim" + (kind === "err" ? " err" : "");
    el.innerHTML = '<span class="t-ic">' + (kind === "err" ? "⚠" : "✓") + '</span><span></span><span class="tclose">✕</span>';
    el.children[1].textContent = msg;
    el.querySelector(".tclose").onclick = function () { el.remove(); };
    // The sequence was already counted and never exposed. Stamping it lets
    // anything watching toasts wait for a NEW one, rather than matching a
    // previous toast still inside its 3.2s lifetime — which is a race the e2e
    // suite actually lost: its second waitForToast resolved against the first
    // toast, and the reload that followed aborted the request whose toast it
    // thought it had seen.
    var id = nextSeq();
    el.dataset.seq = String(id);
    box.appendChild(el);
    setTimeout(function () { el.remove(); }, kind === "err" ? 6000 : 3200);
  }
  window.addEventListener("toast", function (e) { toast(e.detail.value !== undefined ? e.detail.value : e.detail, "ok"); });
  window.addEventListener("toast-error", function (e) { toast(e.detail.value !== undefined ? e.detail.value : e.detail, "err"); });
  // ---------- inline errors ----------
  // c.fail sends 400 with an HX-Doze-Error header. htmx's default for 4xx is
  // swap:false, so that body would never reach the DOM — but beforeSwap fires
  // regardless of shouldSwap, which is the hook that lets us place it ourselves,
  // next to the control that failed, without letting it near the success target
  // the request was aimed at.
  function clearErr(root) {
    (root || document).querySelectorAll(".err[data-doze-err]").forEach(function (el) { el.remove(); });
  }
  // The ladder, most specific first. A surface opts in with data-err-slot;
  // otherwise a form takes it at the end, and failing that the nearest
  // meaningful block gets it.
  function errSlot(elt) {
    if (!elt || !elt.closest) return null;
    var opt = elt.closest("[data-err-slot]");
    if (opt) {
      var sel = opt.getAttribute("data-err-slot");
      var t = sel ? document.querySelector(sel) : opt;
      if (t) return { host: t, how: "append" };
    }
    var form = elt.closest("form");
    if (form) return { host: form, how: "append" };
    var block = elt.closest(".panel, .det-b, .sub-item, .form-page");
    if (block) return { host: block, how: "prepend" };
    return null;
  }
  function placeError(elt, html) {
    var slot = errSlot(elt);
    if (!slot) return false;
    clearErr(slot.host);
    var box = document.createElement("div");
    box.innerHTML = html;
    var node = box.firstElementChild;
    if (!node) return false;
    node.setAttribute("data-doze-err", "1");
    if (slot.how === "prepend") slot.host.insertBefore(node, slot.host.firstChild);
    else slot.host.appendChild(node);
    node.scrollIntoView({ block: "nearest" });
    try { node.focus(); } catch (e) {}
    return true;
  }
  document.addEventListener("htmx:beforeRequest", function (e) {
    var slot = errSlot(e.detail.elt);
    if (slot) clearErr(slot.host);
  });
  document.addEventListener("htmx:beforeSwap", function (e) {
    var x = e.detail.xhr;
    if (!x || x.getResponseHeader("HX-Doze-Error") !== "1") return;
    // Never let an error reach the success target.
    e.detail.shouldSwap = false;
    // isError stays true on purpose: detail.successful must remain false, or
    // every @htmx:after-request="if(successful) ..." in the templates fires on a
    // failure and resets a form the user still needs.
    if (placeError(e.detail.requestConfig.elt, x.responseText)) x._dozeHandled = true;
  });

  document.addEventListener("htmx:responseError", function (e) {
    // Placed inline already? Then a toast would be a duplicate. This stays as the
    // fallback for network errors, 5xx, and any surface the ladder could not find
    // a home in — so it is strictly never worse than before.
    if (e.detail.xhr && e.detail.xhr._dozeHandled) return;
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
    // The QUESTION goes in the message, and the title stays the static "Are you
    // sure?" the markup ships with. Making the question the title instead left
    // #confirm-msg fed only by a data-confirm-detail attribute that no template
    // sets — so every confirm dialog rendered with an empty, hidden message and
    // the question as its heading.
    //
    // That has been true since the keyboard-accessibility pass and nothing
    // caught it, because the e2e suite that asserts it was not being run.
    // aria-labelledby still points at the title, which is now a stable label
    // rather than a sentence that changes per button.
    var msgEl = document.getElementById("confirm-msg");
    msgEl.textContent = q;
    msgEl.hidden = false;
    confirmBox.hidden = false;
    var yes = document.getElementById("confirm-yes"), no = document.getElementById("confirm-no");
    function close() { if (window.dozeTrap) dozeTrap(confirmBox, false); confirmBox.hidden = true; yes.onclick = no.onclick = confirmBox.onclick = null; document.removeEventListener("keydown", onKey); }
    function onKey(ev) { if (ev.key === "Escape") close(); }
    yes.onclick = function () { close(); e.detail.issueRequest(true); };
    no.onclick = close;
    confirmBox.onclick = function (ev) { if (ev.target === confirmBox) close(); };
    document.addEventListener("keydown", onKey);
    yes.focus();
    if (window.dozeTrap) dozeTrap(document.getElementById("confirm"), true);
  });

  // ---------- command palette ----------
  var pal = document.getElementById("palette"), palQ = document.getElementById("pal-q"), palList = document.getElementById("pal-list");
  var palItems = [], palSel = 0;
  // The catalogue comes from the server. These were four hand-maintained arrays
  // — NAV, ACTS, KIND and SVCSET — each covering nine of thirteen services, and
  // one of them still listed "Flows", a page deleted commits earlier. Anything
  // hand-kept beside a router drifts from it.
  var CAT = { nav: [], acts: [], kinds: {} };
  fetch(PREFIX + "/api/palette").then(function (r) { return r.json(); })
    .then(function (c) { if (c) CAT = c; })
    .catch(function () {});

  // ---------- recently visited resources ----------
  // The palette pins your last few resource pages on top: the queue you just
  // left costs ⌘K ⏎ instead of retyping its name.
  // SVCSET gates which pages count as a "resource" for recents. Derived from the
  // catalogue rather than listed again — it used to name nine services, so a
  // visit to a Kinesis stream or an IAM role was never remembered.
  var SVCSET = {};
  fetch(PREFIX + "/api/palette").then(function (r) { return r.json(); })
    .then(function (c) { (c.nav || []).forEach(function (n) { if (n.s) SVCSET[n.s] = 1; }); })
    .catch(function () {});
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
    var fixed = (CAT.nav || []).concat(CAT.acts || []);
    palItems = recents.concat(fixed);
    renderPal();
    palQ.focus();
    if (window.dozeTrap) dozeTrap(document.getElementById("palette"), true);
    fetch(PREFIX + "/api/resources").then(function (r) { return r.json(); }).then(function (rs) {
      var seen = {};
      recents.forEach(function (x) { seen[x.u] = 1; });
      var fresh = (rs || []).filter(function (r) { return !seen[r.u]; })
        .map(function (r) { r.k = (CAT.kinds || {})[r.s] || r.s; return r; });
      palItems = recents.concat(fresh).concat(fixed);
      renderPal();
    });
  }
  // Ordered-subsequence match, so "emd" finds "emails-dlq". Exact substring
  // still ranks first, so literal typing always wins over a lucky subsequence.
  function subseq(hay, q) {
    var i = 0;
    for (var j = 0; j < hay.length && i < q.length; j++) if (hay[j] === q[i]) i++;
    return i === q.length;
  }
  function palFiltered() {
    var q = palQ.value.toLowerCase().trim();
    if (!q) return palItems.slice(0, 10);
    // Searching the service key too means typing "sqs" finds the queues.
    var exact = [], fuzzy = [];
    palItems.forEach(function (it) {
      var hay = (it.n + " " + (it.k || "") + " " + (it.s || "")).toLowerCase();
      if (hay.indexOf(q) >= 0) exact.push(it);
      else if (subseq(hay, q)) fuzzy.push(it);
    });
    return exact.concat(fuzzy).slice(0, 10);
  }

  // An ARN pasted out of a stack trace resolves to its page. The parse lives in
  // Go — it is the same table every link in the console is built from, and a
  // second copy here is a second thing that can be wrong.
  var resolveTimer = null;
  function maybeResolve() {
    var q = palQ.value.trim();
    clearTimeout(resolveTimer);
    if (q.indexOf("arn:") !== 0 && q.indexOf("://") < 0) return;
    resolveTimer = setTimeout(function () {
      fetch(PREFIX + "/api/resolve?id=" + encodeURIComponent(q))
        .then(function (r) { return r.json(); })
        .then(function (ref) {
          if (!ref || !ref.u || palQ.value.trim() !== q) return;
          palItems = [{ s: ref.s, n: ref.n, u: ref.u, k: ref.k || "resource" }].concat(palItems);
          renderPal();
        }).catch(function () {});
    }, 180);
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
  function closePalette() {
    if (window.dozeTrap) dozeTrap(document.getElementById("palette"), false); if (pal) pal.hidden = true; }
  var opener = document.getElementById("palette-open");
  if (opener) opener.addEventListener("click", openPalette);
  if (palQ) palQ.addEventListener("input", function () { palSel = 0; renderPal(); maybeResolve(); });
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
        if (cur.hasAttribute("data-live-paused")) return; // user hit pause
        var url = cur.getAttribute("data-live");
        if (!url) return;
        // Plain outerHTML, not morph — a deliberate retreat, recorded so the
        // next person does not re-fight it. Morphing here never worked: for
        // the whole life of this feature hx-ext was absent so "morph" fell
        // back to innerHTML and nested each region inside itself on first
        // change. Activating it on <body> broke UNRELATED swaps (idiomorph
        // claims every style it does not recognise as inline, which is also
        // how an OOB span's attributes leaked into its target), and scoping
        // it to the regions made idiomorph consume the element outright. A
        // full replace costs scroll position inside a region on the ticks
        // where content actually changed — the 204 no-change path, which is
        // most ticks, swaps nothing at all.
        var swap = cur.getAttribute("data-live-swap") || "outerHTML";
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
      // The expired wording belongs to the caller. "any moment" is right for
      // a function about to go cold, and wrong for a message whose visibility
      // timeout has lapsed, which is "visible again" — same timer, different
      // fact.
      cd.textContent = left > 0 ? fmtLeft(left)
        : (host.getAttribute("data-sleep-done") || "any moment");
    });
  }, 1000);

  // ---------- slow-request bar ----------
  // The button spinner answers "did my click land". It does not answer "is this
  // still going" for the two operations slow enough to read as a hang: a
  // DynamoDB scan and an SQS redrive. 300ms is the threshold because anything
  // faster reads as a flash of chrome rather than as progress.
  var reqBar = null, reqTimer = null, reqDepth = 0;
  function bar() {
    if (!reqBar) {
      reqBar = document.createElement("div");
      reqBar.className = "reqbar";
      document.body.appendChild(reqBar);
    }
    return reqBar;
  }
  document.addEventListener("htmx:beforeRequest", function (e) {
    if ((e.detail.requestConfig || {}).verb === "get") return;
    reqDepth++;
    if (reqTimer) return;
    reqTimer = setTimeout(function () { bar().classList.add("on"); }, 300);
  });
  document.addEventListener("htmx:afterRequest", function (e) {
    if ((e.detail.requestConfig || {}).verb === "get") return;
    if (--reqDepth > 0) return;
    reqDepth = 0;
    clearTimeout(reqTimer); reqTimer = null;
    if (reqBar) reqBar.classList.remove("on");
  });

  // ---------- late-form guard ----------
  // A form carrying hx-post must never fall through to its native submit.
  //
  // There is a window between htmx inserting swapped-in content and binding
  // its listeners. A click landing inside it takes the browser's default path,
  // and for these forms — no action, no method — the default is a GET
  // NAVIGATION to the current URL with the fields as query params: the page
  // reloads, unsaved state evaporates, and the request the button promised
  // never happens. The KMS one-click verify lost this race on every suite run
  // (trace showed GET /kms/{id}?signature=... where a POST should have been),
  // and any human on a slow enough machine can lose it too.
  //
  // Bound forms are untouched: htmx's own handler runs first and calls
  // preventDefault, so defaultPrevented tells the two cases apart. An unbound
  // form is bound on the spot and its submit replayed, so the click still does
  // what it said rather than becoming a no-op.
  document.addEventListener("submit", function (e) {
    var f = e.target;
    if (!f || !f.matches || !f.matches("form[hx-post],form[hx-get],form[hx-put],form[hx-patch],form[hx-delete]")) return;
    if (e.defaultPrevented) return; // htmx got there; nothing to rescue
    e.preventDefault();
    if (window.htmx) { htmx.process(f); htmx.trigger(f, "submit"); }
  });
  // The same race, button flavour. A bare hx-post BUTTON (no form — the
  // one-click "Decrypt this →" shape) has no native fallback, so a click in
  // the unbound window is a silent no-op: the button simply does nothing and
  // there is no navigation to even notice. defaultPrevented cannot tell the
  // cases apart here (htmx does not preventDefault plain clicks), so this
  // checks htmx's own processed marker — internal data with an initHash —
  // and rescues only elements htmx has genuinely not seen.
  document.addEventListener("click", function (e) {
    if (!window.htmx || !e.target || !e.target.closest) return;
    var c = e.target.closest("[hx-post],[hx-get],[hx-put],[hx-patch],[hx-delete]");
    if (!c || c.tagName === "FORM") return;
    if (c.form || c.closest("form")) return; // form members ride the submit guard
    var d = c["htmx-internal-data"];
    if (d && d.initHash) return; // bound: htmx's own listener already fired
    htmx.process(c);
    htmx.trigger(c, "click");
  });

  // ---------- double-submit guard ----------
  // htmx:beforeSend, NOT beforeRequest: the payload is serialized between the
  // two, and a control disabled before serialization drops out of the body.
  //
  // hx-disabled-elt="this" is not usable here — it is inheritable, but "this"
  // resolves to the ancestor CARRYING the attribute, not to the actuator, so a
  // body-level declaration would disable the wrapper and leave the button live.
  function submitControls(elt) {
    if (!elt || !elt.tagName) return [];
    if (elt.tagName === "BUTTON" || elt.tagName === "INPUT") return [elt];
    return Array.prototype.slice.call(
      elt.querySelectorAll('button[type="submit"], button:not([type]), input[type="submit"]'));
  }
  document.addEventListener("htmx:beforeSend", function (e) {
    if ((e.detail.requestConfig || {}).verb === "get") return;
    var elt = e.detail.elt;
    var ctrls = submitControls(elt).filter(function (c) { return !c.disabled; });
    if (!ctrls.length) return;
    ctrls.forEach(function (c) { c.disabled = true; });
    elt._dozeLocked = ctrls;
  });
  function unlock(e) {
    var elt = e.detail && e.detail.elt;
    if (!elt || !elt._dozeLocked) return;
    // Guard against a swap having replaced the button underneath us.
    elt._dozeLocked.forEach(function (c) { if (document.contains(c)) c.disabled = false; });
    elt._dozeLocked = null;
  }
  document.addEventListener("htmx:afterRequest", unlock);
  document.addEventListener("htmx:sendError", unlock);
  document.addEventListener("htmx:timeout", unlock);

  // ---------- filter matched nothing ----------
  // The fourth empty state, and the only one that looked like a bug: rows exist,
  // the filter matches none of them, and the pane goes blank with no server
  // involvement at all. Counting what is actually visible rather than
  // re-implementing the match means this cannot disagree with the rows.
  function syncNoMatch() {
    document.querySelectorAll(".listpane").forEach(function (pane) {
      var note = pane.querySelector(".lp-nomatch");
      if (!note) return;
      var input = pane.querySelector(".filter input");
      var q = input ? input.value.trim() : "";
      var rows = pane.querySelectorAll(".lp-scroll .li");
      var visible = 0;
      rows.forEach(function (r) { if (r.offsetParent !== null) visible++; });
      note.hidden = !(q && rows.length && visible === 0);
    });
  }
  // React to the rows actually changing visibility rather than to the keystroke
  // that will eventually cause it. Alpine applies x-show after the input event,
  // so anything scheduled off the keystroke reads the state as it was before
  // filtering — which is why this checked and always found rows still visible.
  // An observer on the display attribute is correct by construction, and it also
  // covers rows hidden by anything other than typing.
  var noMatchQueued = false;
  function queueNoMatch() {
    if (noMatchQueued) return;
    noMatchQueued = true;
    requestAnimationFrame(function () { noMatchQueued = false; syncNoMatch(); });
  }
  function watchPanes() {
    document.querySelectorAll(".listpane .lp-scroll").forEach(function (scroll) {
      if (scroll.__noMatchObserved) return;
      scroll.__noMatchObserved = true;
      new MutationObserver(queueNoMatch).observe(scroll, {
        subtree: true, childList: true, attributes: true, attributeFilter: ["style", "class"],
      });
    });
    queueNoMatch();
  }
  document.addEventListener("DOMContentLoaded", watchPanes);
  document.addEventListener("htmx:afterSwap", watchPanes);
  watchPanes();

  // ---------- after an in-place navigation ----------
  // c.redirect now swaps #workspace instead of reloading the window. Five things
  // a full page load used to do for free have to be done here.
  var lastPath = location.pathname;
  function serviceOf(p) {
    var seg = p.replace(PREFIX, "").split("/").filter(Boolean);
    return seg.length ? seg[0] : "";
  }
  document.addEventListener("htmx:afterSettle", function (e) {
    if (location.pathname === lastPath) return;
    var from = lastPath, to = location.pathname;
    lastPath = to;

    // 1. The filter box is a GLOBAL Alpine store, so a full reload used to clear
    //    it. In place it survives — navigate S3 -> SQS with "log" typed and
    //    every queue is hidden, which looks like a broken page rather than an
    //    active filter. Clear it when the service changes, keep it when only the
    //    selected resource does.
    if (serviceOf(from) !== serviceOf(to)) {
      try { if (window.Alpine) window.Alpine.store("filter").q = ""; } catch (err) {}
      document.querySelectorAll(".listpane .filter input").forEach(function (i) { i.value = ""; });
    }
    // 2. Focus lands on <body> after a swap, so keyboard flow dies and a screen
    //    reader announces nothing. Move it to the page title and say where we are.
    var t = document.querySelector(".det-title");
    if (t) {
      t.setAttribute("tabindex", "-1");
      try { t.focus({ preventScroll: true }); } catch (err) {}
      announce(t.textContent.trim());
    }
    // 3. #workspace carries hx-swap="... show:none", so htmx never scrolls.
    //    Right for a same-page re-render, wrong when the resource changed.
    if (from !== to) window.scrollTo(0, 0);
    // 4 and 5 need nothing: dozeEditor.upgradeAll and setupLive already re-arm
    //    on afterSwap, and CodeMirror instances in the discarded subtree hold no
    //    document-level listeners, so they are collectable.
  });

  // The flash banner only reaches the page on the non-htmx fallback path now,
  // where it arrives as ?flash= in the URL. Two problems came with that and both
  // outlive the transport change: it never dismissed itself, and the parameter
  // stayed in the address bar, so a refresh or a back navigation re-showed a
  // success that had already happened.
  function tidyFlash() {
    // No auto-remove. Both banners carry a dismiss button and both live inside
    // #workspace, so navigating away clears them; a timer on top of that only
    // takes the message away while you are still reading it, which is the
    // complaint that moved these off toasts in the first place.
    if (location.search.indexOf("flash=") >= 0) {
      try {
        var u = new URL(location.href);
        u.searchParams.delete("flash");
        history.replaceState(history.state, "", u.pathname + u.search + u.hash);
      } catch (err) {}
    }
  }
  document.addEventListener("DOMContentLoaded", tidyFlash);
  document.addEventListener("htmx:afterSettle", tidyFlash);

  // A polite live region, so an in-place navigation is announced the way a page
  // load was.
  var liveRegion = null;
  function announce(msg) {
    if (!msg) return;
    if (!liveRegion) {
      liveRegion = document.createElement("div");
      liveRegion.setAttribute("aria-live", "polite");
      liveRegion.setAttribute("aria-atomic", "true");
      liveRegion.className = "sr-only";
      document.body.appendChild(liveRegion);
    }
    liveRegion.textContent = "";
    setTimeout(function () { liveRegion.textContent = msg; }, 60);
  }

  // The flash arrives as a trigger rather than in the URL, and lands as a
  // banner at the top of the workspace — AWS's flashbar — rather than a corner
  // toast. A toast was the wrong container for this: it carries the RESULT of
  // something you just did, and 3.2 seconds is routinely less time than it
  // takes to look up from the button you pressed. The banner stays until you
  // dismiss it or navigate away.
  //
  // Rendering is deferred to the next settle rather than done inline, and that
  // is not defensive coding — it is required. A mutation that redirects sends
  // HX-Trigger and HX-Location on the SAME response; htmx fires the trigger
  // first, then issues the follow-up GET whose outerHTML swap replaces
  // #workspace wholesale. A banner inserted when the event fires is destroyed
  // by that swap a moment later. So the message is queued and painted once the
  // page it belongs to has settled.
  //
  // The timer is the other half: a partial mutation flashes WITHOUT navigating,
  // so no settle is coming and the queue would sit there unpainted.
  var pendingFlash = null;
  function paintFlash() {
    if (!pendingFlash) return;
    var f = pendingFlash;
    pendingFlash = null;
    var host = document.getElementById("workspace") || document.body;
    // One at a time. Stacking is AWS's behaviour, but AWS's messages come from
    // many sources; ours all come from the thing you just clicked, so a stack
    // is just the same success said twice.
    var prev = host.querySelector(":scope > .flash");
    if (prev) prev.remove();
    var el = document.createElement("div");
    // Same id as the server-rendered banner in layout.html. There are two ways
    // a flash reaches the page — this one for htmx, and ?flash= for the plain
    // form path — and they were rendering the same thing under different
    // identities, so anything addressing #flashbar only worked on one of them.
    el.id = "flashbar";
    // Same monotonic sequence as toasts. A toast and a flashbar are the same
    // thing — feedback for an action — delivered in two shapes, and anything
    // waiting for "the next piece of feedback" needs one counter across both.
    el.dataset.seq = String(nextSeq());
    el.className = "flash anim" + (f.sticky ? " flash-sticky" : "");
    el.setAttribute("role", "status");
    el.innerHTML = '<span class="fl-msg"></span>' +
      (f.sticky ? '<button class="copy-btn" title="Copy">copy</button>' : "") +
      '<button class="icon-btn fl-x" title="Dismiss">&times;</button>';
    el.querySelector(".fl-msg").textContent = f.msg;
    if (f.sticky) {
      el.querySelector(".copy-btn").addEventListener("click", function () {
        navigator.clipboard && navigator.clipboard.writeText(f.msg);
      });
    }
    el.querySelector(".fl-x").addEventListener("click", function () { el.remove(); });
    host.insertBefore(el, host.firstChild);
  }
  function queueFlash(e, sticky) {
    var msg = String(e.detail && (e.detail.value !== undefined ? e.detail.value : e.detail) || "");
    if (!msg) return;
    pendingFlash = { msg: msg, sticky: sticky };
    setTimeout(paintFlash, 120); // no navigation coming — paint it anyway
  }
  window.addEventListener("doze:flash", function (e) { queueFlash(e, false); });
  // A value that cannot be retrieved again also gets a copy button.
  window.addEventListener("doze:flash-sticky", function (e) { queueFlash(e, true); });
  document.addEventListener("htmx:afterSettle", paintFlash);

  // ---------- div-buttons ----------
  // A div carrying hx-get is a button to the user and nothing to a keyboard.
  // The wire was the worst case: its rows are divs, so the console's flagship
  // surface was mouse-only. <button> is not available — .tr-row contains a
  // copy-as-curl button, and nested interactives are invalid HTML that
  // browsers hoist apart — so rows get role and tabindex in the template and
  // one delegated handler here. .click() fires both the hx-get and the Alpine
  // @click, so selection and the drawer fetch work with no per-surface code.
  document.addEventListener("keydown", function (e) {
    if (e.key !== "Enter" && e.key !== " ") return;
    var t = e.target;
    if (!t || !t.matches || !t.matches('[role="button"]:not(button):not(a)')) return;
    e.preventDefault();
    t.click();
  });
  // ---------- focus trap ----------
  // The drawers and the confirm dialog already handled Escape and a scrim
  // click; what none of them did was focus. Tab escaped into the page behind,
  // and nothing restored focus on close — so a keyboard user opening a drawer
  // was silently dropped into a document they could not see.
  var trapped = null, trapReturn = null;
  function focusables(el) {
    return Array.prototype.filter.call(
      el.querySelectorAll('a[href],button:not([disabled]),input:not([disabled]),select,textarea,[tabindex]:not([tabindex="-1"])'),
      function (e) { return e.offsetParent !== null; });
  }
  function onTrapKey(e) {
    if (e.key !== "Tab" || !trapped) return;
    var f = focusables(trapped);
    if (!f.length) return;
    var first = f[0], last = f[f.length - 1];
    if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
    else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
  }
  window.dozeTrap = function (el, open) {
    if (open) {
      if (trapped === el) return;
      trapReturn = document.activeElement;
      trapped = el;
      document.addEventListener("keydown", onTrapKey, true);
      setTimeout(function () { var f = focusables(el); (f[0] || el).focus(); }, 0);
    } else if (trapped === el) {
      trapped = null;
      document.removeEventListener("keydown", onTrapKey, true);
      if (trapReturn && document.contains(trapReturn)) trapReturn.focus();
      trapReturn = null;
    }
  };
  // ---------- form labels ----------
  // 130 labels, none with a for= and only 19 wrapping their control, and
  // exactly one id= on an input in all the templates. So a screen reader had
  // nothing to announce for most fields, and clicking a label focused nothing.
  //
  // Doing this in the template would mean a {{define "field"}} that renders the
  // control itself — html/template has no block-passing — which is enumerating
  // every input variant and rewriting 110 sites for the same result. This pass
  // fixes all of them at once. It is JS-dependent accessibility, which is
  // normally a compromise; here the console is already non-functional without
  // JS, since htmx drives every mutation.
  var fieldSeq = 0;
  function linkLabels(root) {
    (root || document).querySelectorAll(".field").forEach(function (f) {
      var lab = f.querySelector("label");
      var ctl = f.querySelector("input,select,textarea");
      if (!lab || !ctl || lab.htmlFor || lab.contains(ctl)) return;
      if (!ctl.id) ctl.id = "f" + (++fieldSeq);
      lab.htmlFor = ctl.id;
      var hint = f.querySelector(".dim,.hint,.mini-note");
      if (hint) {
        if (!hint.id) hint.id = ctl.id + "-h";
        ctl.setAttribute("aria-describedby", hint.id);
      }
    });
  }
  document.addEventListener("DOMContentLoaded", function () { linkLabels(); });
  document.addEventListener("htmx:afterSwap", function (e) { linkLabels(e.target); });
  // Clearing has to go through the same store the rows filter on, not just the
  // input, or the rows stay hidden while the box looks empty.
  function clearFilter(from) {
    var pane = from && from.closest ? from.closest(".listpane") : document.querySelector(".listpane");
    var input = pane && pane.querySelector(".filter input");
    if (input) input.value = "";
    try { if (window.Alpine) window.Alpine.store("filter").q = ""; } catch (e) {}
    if (input) input.dispatchEvent(new Event("input", { bubbles: true }));
    if (input) input.focus();
  }

  window.dozeShell = { toast: toast, openPalette: openPalette, clearFilter: clearFilter };
})();
