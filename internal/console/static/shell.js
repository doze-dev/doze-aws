// shell.js — workbench chrome: theme, toasts, copy, styled confirms, command
// palette, keyboard navigation, and live-region polling helpers. Everything
// here lives OUTSIDE #workspace so it survives every content swap.
(function () {
  "use strict";
  var PREFIX = (document.querySelector('link[rel="icon"]') || {}).href
    ? new URL(document.querySelector('link[rel="icon"]').href).pathname.replace(/\/static\/favicon\.svg$/, "")
    : "/_console";

  // ---------- Alpine glue ----------
  document.addEventListener("htmx:after:swap", function () {
    // ctx.target is the OLD detached node for outerHTML swaps; upgrade
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
  // Chrome is looked up when it is used and listened to from the document. A
  // history restore can swap <body> and detach every node captured at load; a
  // listener on a detached node never fires again, and a captured reference
  // points at nothing.
  function seg() { return document.getElementById("appearance"); }
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
    var group = seg();
    if (!group) return;
    var buttons = group.querySelectorAll("button");
    for (var i = 0; i < buttons.length; i++) {
      buttons[i].setAttribute("aria-pressed", String(buttons[i].dataset.mode === mode));
    }
  }
  document.addEventListener("click", function (e) {
    var b = e.target.closest && e.target.closest("#appearance button[data-mode]");
    if (b) applyMode(b.dataset.mode);
  });
  applyMode(currentMode());
  // A swapped-in body arrives with the server's idea of the pressed state.
  document.addEventListener("htmx:after:history:restore", function () { applyMode(currentMode()); });

  // ---------- collapsible rail ----------
  // The rail's state is <html data-rail="wide|slim">, and this is the one place
  // that decides it. In order of authority:
  //   1. the user's choice (localStorage "rail", written by the toggle and "["),
  //      which wins everywhere and persists;
  //   2. a window 1180px wide or narrower, where the names are the cheapest 152px
  //      in the shell and the flyout carries the label instead;
  //   3. the page's default, which the server wrote on #workspace
  //      (data-rail-default: labelled on the wire, marks inside a service), with
  //      one exception: on the wire, an open call collapses the rail to marks,
  //      as Wire-Inspect draws it, so the inspector has the room.
  // The pre-paint script in layout.html applies 1 and 2 before first paint; this
  // keeps all three true afterwards: after a boosted navigation (which swaps
  // #workspace and nothing else), on a resize, and as a call opens and closes.
  var narrowMQ = window.matchMedia ? window.matchMedia("(max-width: 1180px)") : null;
  function railChoice() {
    try { var r = localStorage.getItem("rail"); return r === "slim" || r === "wide" ? r : ""; } catch (e) { return ""; }
  }
  function railWanted() {
    var c = railChoice();
    if (c) return c;
    if (narrowMQ && narrowMQ.matches) return "slim";
    var ws = document.getElementById("workspace");
    var d = ws && ws.getAttribute("data-rail-default") === "wide" ? "wide" : "slim";
    // .tr-wrap.open is the wire's own "a call is selected" class.
    if (d === "wide" && document.querySelector("#workspace .tr-wrap.open")) return "slim";
    return d;
  }
  function syncRailState() {
    var want = railWanted();
    if (document.documentElement.getAttribute("data-rail") !== want) document.documentElement.setAttribute("data-rail", want);
  }
  function railSlim() { return document.documentElement.getAttribute("data-rail") === "slim"; }
  function setRail(slim) {
    // A choice is stored, not just applied: it is what wins over the default on
    // every page and after a reload. Both values are written, because
    // "expanded" at 1100px or on a service page is as deliberate as "collapsed".
    try { localStorage.setItem("rail", slim ? "slim" : "wide"); } catch (e) {}
    document.documentElement.setAttribute("data-rail", slim ? "slim" : "wide");
  }
  document.addEventListener("click", function (e) {
    if (e.target.closest && e.target.closest("#rail-toggle")) setRail(!railSlim());
  });
  document.addEventListener("DOMContentLoaded", syncRailState);
  document.addEventListener("htmx:after:swap", syncRailState);
  window.addEventListener("popstate", syncRailState);
  if (narrowMQ && narrowMQ.addEventListener) narrowMQ.addEventListener("change", syncRailState);
  // An open call is an Alpine class toggle, with no event to hear, so the class
  // is watched. One frame's mutations are one check.
  var railFrame = 0;
  new MutationObserver(function () {
    if (railFrame) return;
    railFrame = requestAnimationFrame(function () { railFrame = 0; syncRailState(); });
  }).observe(document.documentElement, { subtree: true, attributes: true, attributeFilter: ["class"] });
  // Flyout labels in slim mode: position fixed on hover and on keyboard focus so
  // the rail's own scroll/overflow can never clip them.
  function placeFlyout(target) {
    if (!railSlim()) return;
    var ri = target.closest && target.closest(".rail .ri");
    if (!ri) return;
    var fly = ri.querySelector(".ri-fly");
    if (!fly) return;
    var r = ri.getBoundingClientRect();
    fly.style.left = (r.right + 8) + "px";
    fly.style.top = (r.top + r.height / 2) + "px";
  }
  document.addEventListener("mouseover", function (e) { placeFlyout(e.target); });
  document.addEventListener("focusin", function (e) { placeFlyout(e.target); });
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
  // htmx 4 has no per-OOB event — htmx:after:swap fires once for the whole
  // response. The swap's task list is on htmx:before:swap instead, and each
  // out-of-band task carries the element it will patch, so this stamps exactly
  // the same ids the old htmx:oobAfterSwap listener did and in one pass rather
  // than one event per badge.
  var patchedAt = Object.create(null);
  document.body.addEventListener("htmx:before:swap", function (e) {
    var tasks = (e.detail && e.detail.tasks) || [];
    for (var i = 0; i < tasks.length; i++) {
      var t = tasks[i];
      if (t.type === "oob" && t.target && t.target.id) patchedAt[t.target.id] = Date.now();
    }
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
      // The status dots ride in the same response, as counts.warn: {svc: "warn"|"bad"}.
      var warn = counts.warn || {};
      document.querySelectorAll(".rail [data-dot]").forEach(function (el) {
        if (Date.now() - (patchedAt[el.id] || 0) < 2500) return;
        var level = warn[el.getAttribute("data-dot")] || "";
        el.className = "ri-dot" + (level ? " " + level : "");
        if (level) {
          el.setAttribute("role", "img");
          el.setAttribute("aria-label", level === "bad" ? "failing" : "needs a look");
        } else {
          el.removeAttribute("role");
          el.removeAttribute("aria-label");
        }
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
    el.setAttribute("role", kind === "err" ? "alert" : "status");
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
    // One success at a time: they all come from the thing you just clicked, so a
    // second one replaces the first rather than saying it twice.
    if (kind !== "err") box.querySelectorAll(".toast:not(.err)").forEach(function (old) { old.remove(); });
    box.appendChild(el);
    // Long enough to look up from the button and read, and it waits while you are
    // reading: hovering or focusing it stops the clock. 3.2s was the complaint that
    // once moved success messages onto a banner.
    var life = kind === "err" ? 10000 : 7000, timer;
    function arm() { clearTimeout(timer); timer = setTimeout(function () { el.remove(); }, life); }
    function hold() { clearTimeout(timer); }
    el.addEventListener("mouseenter", hold);
    el.addEventListener("mouseleave", arm);
    el.addEventListener("focusin", hold);
    el.addEventListener("focusout", arm);
    arm();
  }
  window.addEventListener("toast", function (e) { toast(e.detail.value !== undefined ? e.detail.value : e.detail, "ok"); });
  window.addEventListener("toast-error", function (e) { toast(e.detail.value !== undefined ? e.detail.value : e.detail, "err"); });
  // ---------- inline errors ----------
  // c.fail sends 400 with an HX-Doze-Error header. htmx 4 swaps every status
  // but 204 and 304, so that body WOULD land in the success target if nothing
  // stopped it; the two listeners further down place it next to the control
  // that failed and cancel the swap instead.
  function clearErr(root) {
    (root || document).querySelectorAll(".err[data-doze-err]").forEach(function (el) { el.remove(); });
  }
  // clearSlot clears what a slot's previous refusal left. An "after" slot's
  // refusal is the host's sibling, outside it, so clearing inside the host would
  // leave the last one standing and stack a new one beneath it.
  function clearSlot(slot) {
    if (slot.how === "after") {
      var n = slot.host.nextElementSibling;
      if (n && n.matches(".err[data-doze-err]")) n.remove();
      return;
    }
    if (slot.how === "top") {
      var f = slot.host.firstElementChild;
      if (f && f.matches(".err[data-doze-err]")) f.remove();
      return;
    }
    clearErr(slot.host);
  }
  // The ladder, most specific first. A surface opts in with data-err-slot;
  // otherwise a form takes it at the end, and failing that the nearest
  // meaningful block gets it.
  // onScreen: the element is laid out AND not inside a closed <details>. The
  // second half is not offsetParent's to answer: Chrome lays a closed
  // disclosure's content out under content-visibility, so its offsetParent is
  // set, and a refusal written into the SQS create form's collapsed Advanced
  // section was a refusal nobody saw.
  function onScreen(el) {
    if (!el || el.offsetParent === null) return false;
    for (var d = el.parentElement && el.parentElement.closest("details"); d; d = d.parentElement && d.parentElement.closest("details")) {
      if (!d.open) return false;
    }
    return true;
  }
  function errSlot(elt) {
    if (!elt || !elt.closest) return null;
    var opt = elt.closest("[data-err-slot]");
    if (opt) {
      var sel = opt.getAttribute("data-err-slot");
      var t = sel ? document.querySelector(sel) : opt;
      if (t) return { host: t, how: "append" };
    }
    var form = elt.closest("form");
    if (form) {
      // The opt-in also works DOWNWARD: a form's builder can carry
      // data-err-slot and receive the refusal next to the document that
      // caused it, instead of below the fold at the form's end. closest()
      // above only ascends, so without this rung a descendant slot is
      // unreachable from the submitting form.
      // Of the form's slots, the first VISIBLE one: a mode-switched form
      // (the IAM create page holds a trust builder and a policy builder,
      // one hidden) must not swallow the error into a display:none block.
      var downs = form.querySelectorAll("[data-err-slot]");
      for (var di = 0; di < downs.length; di++) {
        if (!onScreen(downs[di])) continue;
        var dsel = downs[di].getAttribute("data-err-slot");
        var dt = dsel ? document.querySelector(dsel) : downs[di];
        if (dt) return { host: dt, how: "append" };
      }
      return { host: form, how: "append" };
    }
    // A button in a resource's header (Delete, Stop, Disable) has no form and
    // sits above every block below, so a refusal used to fall through to a
    // toast that stripped the message and cut it at 140 characters. It lands
    // directly under the header instead, where the thing that was refused is.
    var head = elt.closest(".det-h");
    if (head) return { host: head, how: "after" };
    var block = elt.closest(".panel, .det-b, .sub-item, .form-page");
    if (block) return { host: block, how: "prepend" };
    // Last rung: the page's own body. A control outside every block above (a
    // list-pane action, a top-strip button) still has a page to say it on, and
    // an error that is only ever a toast is gone in ten seconds.
    // "top" puts it first in the pane's body and clears only what an earlier
    // one put there: the pane may hold errors that belong to a form below.
    var page = document.querySelector(".detail > .det-b, .form-page");
    if (page) return { host: page, how: "top" };
    return null;
  }
  // background: the request was not something the user just did. A live
  // region's poll (htmx.ajax with no source, so htmx names the region itself as
  // the source) and an hx-trigger="every …" tick both repeat for as long as the
  // page is open. Their failures must not draw a box, take focus or clear the
  // errors of the control the user is actually looking at, three seconds apart.
  function background(elt) {
    if (!elt || !elt.getAttribute) return false;
    return elt.hasAttribute("data-live") || /\bevery\b/.test(elt.getAttribute("hx-trigger") || "");
  }
  // unasked: a lazy load (on load, on scroll into view). Its failure is shown
  // where it would have loaded, but it does not pull focus or the scroll to it.
  function unasked(elt) {
    return !!(elt && elt.getAttribute) && /\b(load|intersect|revealed)\b/.test(elt.getAttribute("hx-trigger") || "");
  }
  // A control that carries data-param="VisibilityTimeout|visibility timeout"
  // is the one a refusal naming either is about, and is marked invalid until
  // it is edited or the form is sent again (the design outlines the field).
  // The form is told too (doze:refused), so it can say what the board says
  // beside a refusal: that what was typed is kept.
  function clearRefused(elt) {
    var form = elt && elt.closest && (elt.closest("form") || elt);
    if (!form || !form.querySelectorAll) return;
    form.querySelectorAll("[data-param][aria-invalid]").forEach(function (c) { c.removeAttribute("aria-invalid"); });
    form.dispatchEvent(new CustomEvent("doze:cleared", { bubbles: true }));
  }
  function markRefused(elt, node) {
    var form = elt && elt.closest && (elt.closest("form") || elt);
    if (!form || !form.querySelectorAll) return;
    var text = node.textContent || "", names = [];
    form.querySelectorAll("[data-param]").forEach(function (c) {
      var hit = c.getAttribute("data-param").split("|").some(function (k) { return k && text.indexOf(k) >= 0; });
      if (!hit) return;
      c.setAttribute("aria-invalid", "true");
      names.push(c.getAttribute("name") || "");
    });
    var state = (/\berr-(\w+)/.exec(node.className || "") || [])[1] || "error";
    form.dispatchEvent(new CustomEvent("doze:refused", { bubbles: true, detail: { state: state, fields: names } }));
  }
  document.addEventListener("input", function (e) {
    var t = e.target;
    if (t && t.hasAttribute && t.hasAttribute("data-param")) t.removeAttribute("aria-invalid");
  });
  function placeError(elt, html) {
    if (background(elt)) return true; // handled: by saying nothing
    var slot = errSlot(elt);
    if (!slot) return false;
    clearSlot(slot);
    var box = document.createElement("div");
    box.innerHTML = html;
    var node = box.firstElementChild;
    if (!node) return false;
    node.setAttribute("data-doze-err", "1");
    if (slot.how === "prepend" || slot.how === "top") slot.host.insertBefore(node, slot.host.firstChild);
    else if (slot.how === "after") slot.host.parentNode.insertBefore(node, slot.host.nextSibling);
    else slot.host.appendChild(node);
    markRefused(elt, node);
    if (!unasked(elt)) {
      node.scrollIntoView({ block: "nearest" });
      try { node.focus(); } catch (e) {}
    }
    return true;
  }
  document.addEventListener("htmx:before:request", function (e) {
    var ctx = e.detail.ctx;
    var src = ctx && ctx.sourceElement;
    if (background(src)) return;
    var slot = errSlot(src);
    if (slot) clearSlot(slot);
    clearRefused(src);
  });
  // A refusal placed under a detail header is cleared only by its own slot, and
  // the next thing the user does is usually somewhere else: empty the bucket,
  // then the "bucket is not empty" refusal is still sitting under the header.
  // Any action of the user's that succeeds in the pane retires it.
  document.addEventListener("htmx:after:swap", function (e) {
    var ctx = e.detail.ctx;
    if (!ctx || !ctx.response || ctx.response.status >= 400 || background(ctx.sourceElement)) return;
    document.querySelectorAll(".det-h + .err[data-doze-err]").forEach(function (el) { el.remove(); });
  });
  // c.fail's response is recognised by its header, and handled in two events
  // because htmx 4 fires them in this order:
  //
  //   htmx:after:request  ->  htmx:response:error  ->  htmx:before:swap
  //
  // The inline placement has to happen before response:error, or the toast
  // fallback below cannot tell that the error already has a home and every
  // failure shows up twice. htmx 2 ordered these the other way round and the
  // placement rode on beforeSwap; moving it to after:request is the whole
  // reason this is two listeners rather than one.
  // isConsolePage: the response is a whole console document, which the server
  // sends for its own not-found and failed-load pages.
  function isConsolePage(ctx) {
    return !dozeError(ctx) && typeof ctx.text === "string" && /^\s*<!DOCTYPE html>/i.test(ctx.text);
  }
  function dozeError(ctx) {
    return ctx && ctx.response && ctx.response.headers.get("HX-Doze-Error") === "1";
  }
  document.addEventListener("htmx:after:request", function (e) {
    var ctx = e.detail.ctx;
    if (!dozeError(ctx)) return;
    // The status stays 400, so every template's
    // @htmx:after:request="if(ctx.response.status < 400) ..." still reads a
    // failure and does not reset a form the user still needs.
    if (placeError(ctx.sourceElement, ctx.text)) ctx._dozeHandled = true;
  });
  document.addEventListener("htmx:before:swap", function (e) {
    var ctx = e.detail.ctx;
    // htmx 4 swaps every status but 204 and 304, so a 5xx that is not ours (a
    // plain-text body, a proxy's page) would be written into the pane the
    // action was aimed at and leave it blank. Nothing here is meant to render
    // an error response into a success target: errors are drawn by the
    // response:error listener below, where the action was.
    // One exception: the console's own page (a 404 for a boosted navigation to a
    // deleted resource) is a document meant to be swapped, not an error body.
    if (ctx && ctx.response && ctx.response.status >= 400 && !isConsolePage(ctx)) { e.preventDefault(); return; }
    if (!dozeError(e.detail.ctx)) return;
    // Never let an error reach the success target. htmx 2 took shouldSwap=false
    // for this; htmx 4 cancels a swap by cancelling the event, and that cancels
    // every task in it — main and OOB alike — which is what we want.
    //
    // It matters more than it did: htmx 2 declined to swap 4xx at all, so this
    // was belt and braces. htmx 4's noSwap default is [204, 304], so a 400
    // WOULD land in the success target if this listener stopped firing.
    e.preventDefault();
  });

  document.addEventListener("htmx:response:error", function (e) {
    // Placed inline already? Then a toast would be a duplicate. This stays as the
    // fallback for network errors, 5xx, and any surface the ladder could not find
    // a home in — so it is strictly never worse than before.
    var ctx = e.detail.ctx;
    if (!ctx || ctx._dozeHandled) return;
    // A whole console page is swapped in, not described.
    if (isConsolePage(ctx)) return;
    var raw = (ctx.text || "Request failed");
    // The server HTML-escapes error bodies; decode entities first so the message
    // regex matches and the toast shows real quotes/brackets, not "&#34;".
    var ta = document.createElement("textarea");
    ta.innerHTML = raw;
    raw = ta.value;
    var m = raw.match(/<Message>([^<]+)|"message"\s*:\s*"([^"]+)"/i);
    var msg = (m && (m[1] || m[2])) || raw.replace(/<[^>]*>/g, " ").replace(/\s+/g, " ").trim();
    // A failure that is not an HX-Doze-Error (a 5xx, a body that is not ours)
    // is drawn where the action was, in full, like any other. The toast is the
    // fallback for a page with nowhere to put it, and is cut for its width; the
    // inline copy is not. (A connection that never answers does not come here:
    // htmx raises htmx:error for it, and nothing is drawn. That is the "cannot
    // reach doze-aws" banner of the plan's part D, which is not built.)
    var holder = document.createElement("div");
    var box = document.createElement("div");
    box.className = "err err-error";
    box.setAttribute("role", "alert");
    box.tabIndex = -1;
    var head = document.createElement("div");
    head.className = "err-h";
    head.textContent = "Failed";
    var body = document.createElement("p");
    body.className = "err-msg";
    body.textContent = msg || "The request failed";
    box.appendChild(head);
    box.appendChild(body);
    holder.appendChild(box);
    if (placeError(ctx.sourceElement, holder.innerHTML)) return;
    if (msg.length > 140) msg = msg.slice(0, 140) + "…";
    toast(msg, "err");
  });

  // ---------- copy ----------
  document.addEventListener("click", function (e) {
    var el = e.target.closest("[data-copy]");
    if (!el) return;
    navigator.clipboard.writeText(el.getAttribute("data-copy")).then(function () { toast("Copied"); });
  });

  // ---------- the wire dock, collapsed (kit res_wire; board Narrow) ----------
  // Below 1100px the dock is one sentence and a show button. Whether it was
  // shown is the PANE's (.detail.wire-open), not the dock's: the dock is
  // replaced whole on every live refresh, and a flag on it would close itself
  // every few seconds.
  document.addEventListener("click", function (e) {
    var btn = e.target.closest && e.target.closest("[data-wire-toggle]");
    if (!btn) return;
    var pane = btn.closest(".detail");
    if (pane) pane.classList.toggle("wire-open");
  });

  // ---------- styled confirm (intercepts hx-confirm) ----------
  // Verbs a confirm's button can be, and which of them lose something. A verb
  // not listed here reads as "Confirm" until the site names one.
  var CONFIRM_VERBS = {
    Delete: 1, Remove: 1, Revoke: 1, Detach: 1, Stop: 1, Disable: 1, Unsubscribe: 1, Reset: 1,
    Purge: 1, Deregister: 1, Clear: 1, Abort: 1, Schedule: 1, Discard: 1, Empty: 1,
    Execute: 0, Rename: 0, Rescale: 0, Repoint: 0, Redrive: 0, Move: 0, Split: 0, Merge: 0, Enable: 0
  };
  function dozeConfirmParts(q, trigger) {
    // The question ends at the first "?" that is followed by a space or the
    // end AND is not inside the curly quotes a name is written in: an object
    // key may hold "? " of its own (“reports/why? final.txt”).
    q = q.trim();
    var cut = -1, quoted = false;
    for (var qi = 0; qi < q.length; qi++) {
      var ch = q.charAt(qi);
      if (ch === "“") quoted = true;
      else if (ch === "”") quoted = false;
      else if (ch === "?" && !quoted && (qi === q.length - 1 || /\s/.test(q.charAt(qi + 1)))) { cut = qi; break; }
    }
    var title = cut < 0 ? q : q.slice(0, cut + 1), detail = cut < 0 ? "" : q.slice(cut + 1).trim();
    var explicit = trigger && trigger.closest && trigger.closest("[data-confirm-verb]");
    var verb = explicit ? explicit.getAttribute("data-confirm-verb") : "";
    if (!verb) {
      var words = title.split(/\s+/);
      // "Permanently delete ..." is a delete.
      var w = /ly$/.test(words[0]) && words[1] ? words[1].charAt(0).toUpperCase() + words[1].slice(1) : words[0];
      if (Object.prototype.hasOwnProperty.call(CONFIRM_VERBS, w)) verb = w;
    }
    var destructive = verb !== "" && CONFIRM_VERBS[verb] === 1;
    if (explicit && explicit.hasAttribute("data-confirm-destructive")) destructive = explicit.getAttribute("data-confirm-destructive") !== "false";
    // known: the site's verb was read. An unreadable one ("Make this version
    // current?") still gets the cautious button, but it is not known to lose
    // anything, so its cancel is not "Keep it".
    return { title: title, detail: detail, verb: verb || "Confirm", destructive: destructive || verb === "", known: verb !== "" };
  }
  document.addEventListener("htmx:confirm", function (e) {
    var confirmBox = document.getElementById("confirm");
    var q = e.detail.ctx && e.detail.ctx.confirm;
    if (!q || !confirmBox) return;
    e.preventDefault();
    // htmx 4 AWAITS this confirmation, and the request queue does not advance
    // until one of issueRequest/dropRequest is called. htmx 2 simply dropped a
    // request whose confirm was never answered, so dismissing the dialog could
    // be a no-op; here it has to resolve, or the next request on the page
    // queues behind a promise nobody settles. Every exit below goes through
    // settle() for that reason, and settle() is once-only because the backdrop,
    // the Escape key and the No button can all fire for one dialog.
    var settled = false;
    function settle(ok) {
      if (settled) return;
      settled = true;
      if (ok) { e.detail.issueRequest(); return; }
      e.detail.dropRequest();
      // A control that asks on change (a stage's deployment picker) has
      // already changed by the time it asks. Declined, it goes back to what is
      // actually in effect rather than showing a value nothing applied.
      var src = e.target && e.target.closest && e.target.closest("form");
      if (src && /\bchange\b/.test(src.getAttribute("hx-trigger") || "")) {
        src.reset();
        src.querySelectorAll("select").forEach(function (s) { s.dispatchEvent(new Event("ds:sync")); });
      }
    }
    // The dialog says three things: what is being acted on (the title, the
    // question), what that costs (the sentence after it, when the call site
    // wrote one), and the button is the verb. "Confirm" on a delete asks the
    // reader to work out what they are confirming; "Delete" does not.
    //
    // The pieces come from the hx-confirm text itself, so a call site is
    // written once, as a question and what follows it, and no site had to be
    // reshaped. data-confirm-verb on the trigger overrides the first-word
    // reading where the sentence does not begin with the action.
    var parts = dozeConfirmParts(q, e.target);
    var titleEl = document.getElementById("confirm-title");
    var msgEl = document.getElementById("confirm-msg");
    titleEl.textContent = parts.title;
    // The page's own heading is level 1 and the dialog's is the next one down;
    // the element is an h3 in the layout, which skips a level.
    titleEl.setAttribute("role", "heading");
    titleEl.setAttribute("aria-level", "2");
    msgEl.textContent = parts.detail;
    msgEl.hidden = parts.detail === "";
    confirmBox.hidden = false;
    var yes = document.getElementById("confirm-yes"), no = document.getElementById("confirm-no");
    yes.textContent = parts.verb;
    // The cancel button says what it keeps. The boards write "Keep it" on every
    // confirm that destroys something (Sqs-16, S3-12, Kms-13, States) and
    // "Cancel" on the one that does not (Sqs-14, the redrive). So the word is
    // decided here, from the same reading that decides the verb's colour, and
    // not at the ninety call sites. A trigger that names its own (the plural
    // "Keep them" of a purge) is believed.
    var keep = e.target && e.target.closest && e.target.closest("[data-confirm-cancel]");
    no.textContent = keep ? keep.getAttribute("data-confirm-cancel") : parts.destructive && parts.known ? "Keep it" : "Cancel";
    // The verb is a red outline when it destroys something (the boards'
    // "btn danger") and the plain filled button when it does not.
    yes.className = "btn" + (parts.destructive ? " btn-outline danger" : "");
    function close() { if (window.dozeTrap) dozeTrap(confirmBox, false); confirmBox.hidden = true; yes.onclick = no.onclick = confirmBox.onclick = null; document.removeEventListener("keydown", onKey); }
    function dismiss() { close(); settle(false); }
    function onKey(ev) { if (ev.key === "Escape") dismiss(); }
    yes.onclick = function () { close(); settle(true); };
    no.onclick = dismiss;
    confirmBox.onclick = function (ev) { if (ev.target === confirmBox) dismiss(); };
    document.addEventListener("keydown", onKey);
    // The trap is armed BEFORE anything in the dialog is focused: it records
    // document.activeElement as what to give focus back to, and with the
    // order the other way round that was the dialog's own button, which is
    // gone when the dialog closes.
    if (window.dozeTrap) dozeTrap(document.getElementById("confirm"), true);
    else yes.focus();
  });

  // ---------- command palette ----------
  var pal, palQ, palList;
  function palRefs() { pal = document.getElementById("palette"); palQ = document.getElementById("pal-q"); palList = document.getElementById("pal-list"); }
  palRefs();
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
  document.addEventListener("htmx:after:history:push", trackVisit);

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
  document.addEventListener("htmx:after:history:push", syncRail);
  window.addEventListener("popstate", syncRail);

  function openPalette() {
    palRefs();
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
  // The results are two sections, as the Palette board draws them: "Go to"
  // (where things are: your recent pages, the resources, the services and
  // surfaces) and "Create" (what can be made). Arrow keys walk them as one list,
  // in the order they are drawn; the captions are not rows.
  function isMake(it) { return it.k === "create"; }
  function actFor(svc) {
    return (CAT.acts || []).filter(function (a) { return a.s === svc && a.w; })[0];
  }
  // A create with the typed name on it. The address is the create page's own,
  // with the name in the parameter the catalogue says carries it, so the form
  // opens already holding what was typed.
  function typedCreate(a, name) {
    return { s: a.s, w: a.w, name: name, k: "create", typed: true,
             u: a.u + (a.u.indexOf("?") < 0 ? "?" : "&") + (a.p || "name") + "=" + encodeURIComponent(name) };
  }
  // "create queue harbour-x": a verb, a noun the catalogue says can be made, and
  // a name. Nothing else is a typed create, so "create gizmo x" offers nothing.
  function typedFromVerb(raw) {
    var m = /^(?:create|new|make|add|store)\s+(.+)$/i.exec(raw.trim());
    if (!m) return null;
    var rest = m[1].trim(), low = rest.toLowerCase();
    var acts = (CAT.acts || []).filter(function (a) { return a.w; })
      .sort(function (a, b) { return b.w.length - a.w.length; });
    for (var i = 0; i < acts.length; i++) {
      var w = acts[i].w.toLowerCase();
      if (low.indexOf(w + " ") !== 0) continue;
      var name = rest.slice(w.length).trim();
      if (name && !/\s/.test(name)) return typedCreate(acts[i], name);
    }
    return null;
  }
  // The board's other case: a single word that is not (yet) anything, typed
  // while a thing of one kind matches it ("checkout" finds queues), offers a new
  // one of that kind with the word for its name. Not offered for a name that is
  // already there, for a service or a page, or for an ARN or a URL.
  function typedFromMatch(raw, top) {
    var name = raw.trim();
    if (!top || !top.s || !/^\S{2,}$/.test(name) || name.indexOf("arn:") === 0 || name.indexOf("://") >= 0) return null;
    if (top.k === "service" || top.k === "surface" || isMake(top)) return null;
    var a = actFor(top.s);
    if (!a) return null;
    var taken = palItems.some(function (it) { return !isMake(it) && it.k !== "service" && it.k !== "surface" && it.n === name; });
    return taken ? null : typedCreate(a, name);
  }
  function palMatch(q) {
    // Searching the service key too means typing "sqs" finds the queues.
    var ex = [], fz = [], mex = [], mfz = [];
    palItems.forEach(function (it) {
      if (it.q) { if (it.q.toLowerCase() === q) ex.unshift(it); return; }
      var hay = (it.n + " " + (it.k || "") + " " + (it.s || "")).toLowerCase();
      var make = isMake(it);
      if (hay.indexOf(q) >= 0) (make ? mex : ex).push(it);
      else if (subseq(hay, q)) (make ? mfz : fz).push(it);
    });
    var typed = typedFromVerb(palQ.value) || typedFromMatch(palQ.value, ex[0]);
    return { go: ex.concat(fz), make: (typed ? [typed] : []).concat(mex, mfz) };
  }
  // palView is what is shown: ten rows at most, "Go to" first, and the count
  // of everything that matched.
  function palView() {
    var q = palQ.value.toLowerCase().trim();
    var m = q ? palMatch(q) : { go: palItems.filter(function (it) { return !isMake(it); }), make: palItems.filter(isMake) };
    var mk = m.make.slice(0, Math.max(3, 10 - m.go.length)), go = m.go.slice(0, 10 - mk.length);
    return { q: q, go: go, make: mk, items: go.concat(mk), total: m.go.length + m.make.length };
  }
  function palFiltered() { return palView().items; }

  // An ARN pasted out of a stack trace resolves to its page. The parse lives in
  // Go — it is the same table every link in the console is built from, and a
  // second copy here is a second thing that can be wrong.
  var resolveTimer = null;
  function maybeResolve() {
    palRefs();
    var q = palQ.value.trim();
    clearTimeout(resolveTimer);
    if (q.indexOf("arn:") !== 0 && q.indexOf("://") < 0) return;
    resolveTimer = setTimeout(function () {
      fetch(PREFIX + "/api/resolve?id=" + encodeURIComponent(q))
        .then(function (r) { return r.json(); })
        .then(function (ref) {
          if (!ref || !ref.u || palQ.value.trim() !== q) return;
          // q is the ARN itself, which matches no name: the item carries the
          // query that found it, so the filter keeps it.
          palItems = [{ s: ref.s, n: ref.n, u: ref.u, k: ref.k || "resource", q: q }].concat(palItems);
          renderPal();
        }).catch(function () {});
    }, 180);
  }
  function renderPal() {
    palRefs();
    if (!palList) return;
    var v = palView(), items = v.items;
    if (palSel >= items.length) palSel = Math.max(0, items.length - 1);
    palList.innerHTML = "";
    // The count the board draws beside the query: everything that matched, and
    // how many of those are shown when there are more than the ten rows.
    var count = document.getElementById("pal-count");
    if (count) {
      count.textContent = !v.q ? "" :
        (items.length < v.total ? items.length + " of " + v.total : v.total) + " " + (v.total === 1 ? "match" : "matches");
    }
    if (!items.length) { palList.innerHTML = '<div class="pal-empty">Nothing matches</div>'; return; }
    var at = 0;
    function section(label, rows) {
      if (!rows.length) return;
      var cap = document.createElement("div");
      cap.className = "pal-cap";
      cap.textContent = label;
      palList.appendChild(cap);
      rows.forEach(function (it) {
        var i = at++, a = document.createElement("a");
        a.className = "pal-item" + (i === palSel ? " sel" : "");
        a.href = it.u;
        if (it.s) {
          var img = document.createElement("img");
          img.className = "aws-ic sm"; img.alt = ""; img.src = PREFIX + "/static/aws/" + it.s + ".svg";
          a.appendChild(img);
        } else {
          var dot = document.createElement("span");
          dot.className = "pal-dot";
          a.appendChild(dot);
        }
        var nm = document.createElement("span");
        nm.className = "pal-name";
        if (it.typed) {
          nm.appendChild(document.createTextNode("New " + it.w + " called "));
          var typed = document.createElement("span");
          typed.className = "mono"; typed.textContent = it.name;
          nm.appendChild(typed);
        } else {
          paintName(nm, it.n, v.q);
        }
        a.appendChild(nm);
        if (!it.typed) {
          var kind = document.createElement("span");
          kind.className = "pal-kind"; kind.textContent = it.k;
          a.appendChild(kind);
        }
        a.onmouseenter = function () { palSel = i; paintSel(); };
        palList.appendChild(a);
      });
    }
    section("Go to", v.go);
    section("Create", v.make);
    paintSel();
  }
  // The row under the cursor names its key (the board's ⏎); the others do not.
  function paintSel() {
    palRefs();
    if (!palList) return;
    var rows = palList.querySelectorAll(".pal-item");
    for (var i = 0; i < rows.length; i++) {
      var on = i === palSel, k = rows[i].querySelector(".k");
      rows[i].classList.toggle("sel", on);
      if (on && !k) {
        k = document.createElement("span");
        k.className = "k"; k.textContent = "⏎";
        rows[i].appendChild(k);
        rows[i].scrollIntoView({ block: "nearest" });
      } else if (!on && k) k.remove();
    }
  }
  // A name with what was typed underlined in it, as the board draws it: the
  // substring where there is one, else the letters, in order, where the loose
  // match found them. A match that lies in the kind or the service and not in
  // the name underlines nothing.
  function paintName(el, text, q) {
    var low = text.toLowerCase(), runs = [], at = q ? low.indexOf(q) : -1;
    if (at >= 0) runs.push([at, at + q.length]);
    else if (q) {
      var j = 0;
      for (var i = 0; i < low.length && j < q.length; i++) {
        if (low[i] !== q[j]) continue;
        j++;
        var last = runs[runs.length - 1];
        if (last && last[1] === i) last[1] = i + 1; else runs.push([i, i + 1]);
      }
      if (j !== q.length) runs = [];
    }
    var pos = 0;
    runs.forEach(function (r) {
      if (r[0] > pos) el.appendChild(document.createTextNode(text.slice(pos, r[0])));
      var hit = document.createElement("span");
      hit.className = "pal-hit"; hit.textContent = text.slice(r[0], r[1]);
      el.appendChild(hit);
      pos = r[1];
    });
    if (pos < text.length) el.appendChild(document.createTextNode(text.slice(pos)));
  }
  function closePalette() {
    palRefs();
    if (window.dozeTrap) dozeTrap(document.getElementById("palette"), false); if (pal) pal.hidden = true; }
  document.addEventListener("click", function (e) {
    if (e.target.closest && e.target.closest("#palette-open")) openPalette();
    else if (e.target.id === "palette") closePalette();
  });
  document.addEventListener("input", function (e) {
    if (e.target.id === "pal-q") { palSel = 0; renderPal(); maybeResolve(); }
  });

  // ---------- keyboard ----------
  // KEYBAR is every key the console binds, by the name the key bar draws it
  // with, and what it does. The bar (below) draws only keys that are in this
  // table, and a page names its keys in data-keys (keys.go), so a page cannot
  // promise a key that does nothing: the audit that found seven of them
  // (plan/03-shell-fidelity.md 2.4) is why. A handler returns true when it did
  // something. The browser spec (shell.spec.ts, "the key bar") drives every
  // key a bar names, and listpane_keys_test.go checks every name in a template
  // against the keys of this table, so keep one key to a line.
  var KEYBAR = {
    "j": function () { return wireOn() ? wireMove(1) : moveCursor(1); },
    "k": function () { return wireOn() ? wireMove(-1) : moveCursor(-1); },
    "⏎": function () { return wireOn() ? wireOpen() : openCursorRow(); },
    "/": function () { return focusFilter(); },
    "c": function () { return wireInspecting() ? wireCopyCurl() : clickNew(); },
    "→": function () { return wireFollow(); },
    "esc": function () { return wireClose() || cancelPage(); },
    "⌘K": function () { openPalette(); return true; },
    "⌘⏎": function () { return submitCreate(); },
    "tab": function () { return false; /* the browser's own: the next field */ }
  };
  // What a keydown is called on a bar. ⌘K is matched before this table.
  var KEYCAP = { j: "j", k: "k", Enter: "⏎", "/": "/", c: "c", ArrowRight: "→", Escape: "esc" };

  var cursor = -1;
  // The rows that can be moved over are the ones drawn: a filtered-out row, or
  // every row while the switcher's menu is shut, is not a place the cursor can be.
  function listRows() {
    return Array.prototype.slice.call(document.querySelectorAll(".listpane .li[href]")).filter(isShown);
  }
  function resetListCursor() { cursor = -1; }
  function moveCursor(d) {
    // At 1100 and below the rows are a menu. j and k open it, as the pane's own
    // keys would have moved over a column that is not there.
    var pane = switcherPane();
    if (pane && !pane.classList.contains("lp-open")) setSwitcher(pane, true, false);
    var rows = listRows();
    if (!rows.length) return false;
    cursor = Math.min(rows.length - 1, Math.max(0, cursor + d));
    rows.forEach(function (r, i) { r.classList.toggle("cursor", i === cursor); });
    rows[cursor].scrollIntoView({ block: "nearest" });
    return true;
  }

  // ---------- the list pane as a switcher (1100px and below) ----------
  // Board Narrow, decision D5 (plan/03-shell-fidelity.md): the pane is not a
  // column below 1100px. Its head is a strip over the detail with the open
  // resource's name as a button ("name ▾") and "1 of 4 queues" beside it; the
  // button opens the filter and the rows as a menu. The name and the count are
  // read from the rows, so no service supplies either. The keys the menu owns:
  // / opens it on the filter, j and k open it and move, the arrows move from the
  // filter, Enter opens the row under the cursor (or the first one drawn),
  // Escape shuts it. The cursor starts on the open resource.
  var NARROW = window.matchMedia ? window.matchMedia("(max-width: 1100px)") : { matches: false };
  function switcherPane() {
    if (!NARROW.matches) return null;
    var pane = document.querySelector(".listpane");
    return pane && pane.querySelector(".lp-switch") ? pane : null;
  }
  function openSwitcherPane() { return document.querySelector(".listpane.lp-open"); }
  function setSwitcher(pane, open, focusFilterBox) {
    var btn = pane.querySelector(".lp-switch"), input = pane.querySelector(".filter input");
    pane.classList.toggle("lp-open", open);
    if (btn) btn.setAttribute("aria-expanded", open ? "true" : "false");
    pane.querySelectorAll(".li.cursor").forEach(function (r) { r.classList.remove("cursor"); });
    cursor = -1;
    if (open) {
      var at = listRows().indexOf(pane.querySelector(".li.on[href]"));
      cursor = at;
      if (at >= 0) { listRows()[at].classList.add("cursor"); listRows()[at].scrollIntoView({ block: "nearest" }); }
      if (focusFilterBox && input) input.focus();
    } else if (input) {
      // A filter left behind would hide rows the next time the menu opens.
      input.value = "";
      try { if (window.Alpine) window.Alpine.store("filter").q = ""; } catch (e) {}
      input.dispatchEvent(new Event("input", { bubbles: true }));
    }
  }
  function closeSwitcher(refocus) {
    var pane = openSwitcherPane();
    if (!pane) return false;
    var inside = pane.contains(document.activeElement);
    setSwitcher(pane, false, false);
    var btn = pane.querySelector(".lp-switch");
    if (btn && (refocus || inside)) btn.focus();
    return true;
  }
  // The strip's words. Rows that are pages of the service rather than members of
  // its list (IAM's access log, a service's pseudo row, the ghost of a name being
  // typed) are not counted.
  function syncSwitchers() {
    document.querySelectorAll(".listpane").forEach(function (pane) {
      var btn = pane.querySelector(".lp-switch"), of = pane.querySelector(".lp-sw-of");
      if (!btn || !of) return;
      var rows = Array.prototype.slice.call(pane.querySelectorAll(".lp-scroll .li[href]"));
      var items = rows.filter(function (r) { return !r.matches(".lp-pseudo, .lp-page, .lp-ghost"); });
      var on = pane.querySelector(".lp-scroll .li.on[href]");
      var word = function (r) { var nm = r.querySelector(".nm") || r; return nm.textContent.replace(/\s+/g, " ").trim(); };
      var item = btn.getAttribute("data-item") || "item";
      var many = /s$/.test(item) ? item + "es" : item + "s";
      var n = items.length, label = n + " " + (n === 1 ? item : many);
      var at = on ? items.indexOf(on) : -1;
      var name = btn.querySelector(".lp-sw-name"), nameText = on ? word(on) : btn.getAttribute("data-all");
      if (name && name.textContent !== nameText) name.textContent = nameText;
      var ofText = at >= 0 ? (at + 1) + " of " + label : label;
      if (of.textContent !== ofText) of.textContent = ofText;
    });
  }
  document.addEventListener("click", function (e) {
    var btn = e.target.closest ? e.target.closest(".lp-switch") : null;
    if (btn) {
      var pane = btn.closest(".listpane");
      setSwitcher(pane, !pane.classList.contains("lp-open"), true);
      return;
    }
    var open = openSwitcherPane();
    if (!open) return;
    // A row opens its resource (and the swap shuts the menu); anything outside
    // the pane shuts it.
    if (!e.target.closest || !e.target.closest(".listpane") || e.target.closest(".lp-menu a.li")) setSwitcher(open, false, false);
  });
  document.addEventListener("input", function (e) {
    // Typing changes which rows are drawn, so a cursor index would point at another row.
    if (e.target.matches && e.target.matches(".listpane.lp-open .filter input")) {
      cursor = -1;
      document.querySelectorAll(".listpane .li.cursor").forEach(function (r) { r.classList.remove("cursor"); });
    }
  });
  function watchSwitchers() {
    syncSwitchers();
    document.querySelectorAll(".listpane .lp-scroll").forEach(function (scroll) {
      if (scroll.__swObserved) return;
      scroll.__swObserved = true;
      new MutationObserver(syncSwitchers).observe(scroll, { subtree: true, childList: true, attributes: true, attributeFilter: ["class"] });
    });
  }
  document.addEventListener("DOMContentLoaded", watchSwitchers);
  document.addEventListener("htmx:after:swap", watchSwitchers);
  watchSwitchers();
  function openCursorRow() {
    if (cursor < 0) return false;
    var r = listRows()[cursor];
    if (r) r.click();
    return !!r;
  }
  function clickNew() {
    var nb = document.querySelector(".listpane .new-link");
    if (nb) nb.click();
    return !!nb;
  }
  function focusFilter() {
    var sw = switcherPane();
    if (sw) { setSwitcher(sw, true, true); return true; }
    var f = document.querySelector(".listpane .filter input");
    // The wire has no list pane: its filter is the service select above the rows.
    // (select.js hides the native select behind a button with the same label.)
    if (!f && wireOn()) {
      f = document.querySelector("#workspace button[aria-label='Filter by service']") ||
        document.querySelector("#workspace select[aria-label='Filter by service']");
    }
    if (f) { f.focus(); return true; }
    openPalette();
    return true;
  }
  function isShown(el) { return !!el && el.getClientRects().length > 0; }

  // The wire (traffic.html): the rows are a list too, with a cursor of their own
  // and an inspector. The cursor is a call's sequence number, not an index, and
  // is painted again after every poll, because the feed morphs each tick and a
  // morph removes a class the server did not write.
  var wireSeq = null;
  function wireOn() { return !!document.getElementById("traffic-feed"); }
  function wireRows() {
    return Array.prototype.slice.call(document.querySelectorAll("#traffic-feed .tr-row")).filter(isShown);
  }
  function wireInspecting() { return !!document.querySelector(".tr-wrap.open") && isShown(document.querySelector(".t-drawer")); }
  function wirePaint() {
    if (!wireOn()) { wireSeq = null; return; }
    wireRows().forEach(function (r) { r.classList.toggle("cursor", wireSeq !== null && r.getAttribute("data-seq") === wireSeq); });
  }
  function wireMove(d) {
    var rows = wireRows();
    if (!rows.length) return false;
    var at = -1;
    rows.forEach(function (r, i) {
      if (wireInspecting() ? r.classList.contains("sel") : r.getAttribute("data-seq") === wireSeq) at = i;
    });
    var next = Math.min(rows.length - 1, Math.max(0, at + d));
    wireSeq = rows[next].getAttribute("data-seq");
    wirePaint();
    rows[next].scrollIntoView({ block: "nearest" });
    // With a call open, moving is reading the next one.
    if (wireInspecting() && !rows[next].classList.contains("sel")) rows[next].click();
    return true;
  }
  function wireOpen() {
    var row = wireRows().filter(function (r) { return r.getAttribute("data-seq") === wireSeq; })[0];
    if (!row) return false;
    if (!row.classList.contains("sel")) row.click();
    return true;
  }
  function wireCopyCurl() {
    var b = document.querySelector(".t-drawer .td-h .copy-btn");
    if (b) b.click();
    return !!b;
  }
  function wireFollow() {
    if (!wireInspecting()) return false;
    var a = document.querySelector("#traffic-feed .tr-row.sel .res-link");
    if (a) a.click();
    return !!a;
  }
  function wireClose() {
    if (!wireInspecting()) return false;
    try { window.Alpine.$data(document.querySelector(".tr-wrap")).sel = null; } catch (e) { return false; }
    return true;
  }

  // Escape on a create page leaves the form: the page's own Cancel link if it
  // has one, else the service's home (the rail's active item), else back. Not
  // "back" first: a create page opened from a bookmark, or by the palette,
  // would leave the console.
  function cancelPage() {
    var page = document.querySelector("#workspace [data-esc-back]");
    if (!page) return false;
    var link = Array.prototype.slice.call(page.querySelectorAll("a.btn")).filter(function (a) { return /^\s*cancel\s*$/i.test(a.textContent); })[0] ||
      document.querySelector(".rail .ri.on");
    if (link) link.click(); else history.back();
    return true;
  }
  // ⌘⏎ on a create page makes the thing: the form the focus is in, else the
  // page's (a create page has one). Not a page that is not a create page.
  function submitCreate() {
    var page = document.querySelector("#workspace [data-esc-back]");
    if (!page) return false;
    var at = document.activeElement, form = at && at.closest ? at.closest("form") : null;
    if (!form || !page.contains(form)) form = page.querySelector("form");
    if (!form) return false;
    form.requestSubmit();
    return true;
  }
  // A dialog, drawer or palette on top has its own Escape; the page behind must
  // not also leave.
  function overlayOpen() {
    var o = document.querySelectorAll(".overlay, [role=dialog], [role=alertdialog]");
    for (var i = 0; i < o.length; i++) if (!o[i].hidden && isShown(o[i])) return true;
    return !!document.querySelector(".CodeMirror-hints");
  }

  // ---------- the key bar ----------
  // One 28px bar at the foot of every page (#keybar, layout.html), drawn from
  // the last visible data-keys in the workspace: a create form's beats its
  // list pane's, an open inspector's beats the wire's. It is rebuilt when the
  // workspace is swapped and when a declaration appears, changes or is hidden,
  // and only keys in KEYBAR are drawn. "⌘K" is the floor: a page that declares
  // nothing still answers to the palette.
  var KEYS_FLOOR = "⌘K=jump or act";
  function declaredKeys() {
    var els = document.querySelectorAll("#workspace [data-keys]");
    for (var i = els.length - 1; i >= 0; i--) if (isShown(els[i])) return els[i].getAttribute("data-keys") || KEYS_FLOOR;
    return KEYS_FLOOR;
  }
  function parseKeys(spec) {
    var out = [];
    spec.split("|").forEach(function (item) {
      var eq = item.indexOf("=");
      if (eq < 0) return;
      var caps = item.slice(0, eq).split(" ").filter(Boolean);
      if (!caps.length || !caps.every(function (c) { return Object.prototype.hasOwnProperty.call(KEYBAR, c); })) {
        if (window.console) console.warn("the key bar was asked to name a key that is not bound:", item);
        return;
      }
      out.push({ caps: caps, label: item.slice(eq + 1) });
    });
    return out;
  }
  function drawKeyBar() {
    var bar = document.getElementById("keybar");
    if (!bar) return;
    var spec = declaredKeys();
    if (bar.getAttribute("data-spec") === spec) return;
    bar.setAttribute("data-spec", spec);
    bar.textContent = "";
    parseKeys(spec).forEach(function (it) {
      var span = document.createElement("span");
      it.caps.forEach(function (c) {
        var k = document.createElement("span");
        k.className = "k";
        k.textContent = c;
        span.appendChild(k);
      });
      span.appendChild(document.createTextNode(it.label));
      bar.appendChild(span);
    });
  }
  var keyBarQueued = false;
  function syncKeyBar() {
    if (keyBarQueued) return;
    keyBarQueued = true;
    requestAnimationFrame(function () {
      keyBarQueued = false;
      drawKeyBar();
      wirePaint();
      syncGhost();
    });
  }
  var keyBarWatch = null;
  function watchKeyBar() {
    var root = document.querySelector(".body");
    if (!root || keyBarWatch) { syncKeyBar(); return; }
    keyBarWatch = new MutationObserver(syncKeyBar);
    // Attributes that move a declaration in or out of view: its own value, and
    // the style/hidden an x-show or a drawer toggles. Not class: wirePaint
    // writes classes, and watching them would be watching itself.
    keyBarWatch.observe(root, { subtree: true, childList: true, attributes: true, attributeFilter: ["data-keys", "style", "hidden"] });
    syncKeyBar();
  }
  document.addEventListener("DOMContentLoaded", watchKeyBar);
  document.addEventListener("htmx:after:swap", syncKeyBar);

  // The Create board's ghost row: the thing being made, in the list while its
  // name is typed. Any input with data-ghost feeds the store the row reads.
  function syncGhost() {
    try {
      var src = document.querySelector("#workspace [data-ghost]");
      window.Alpine.store("ghost").name = src ? src.value : "";
    } catch (e) {}
  }
  document.addEventListener("input", function (e) {
    if (e.target && e.target.matches && e.target.matches("[data-ghost]")) syncGhost();
  });

  document.addEventListener("keydown", function (e) {
    palRefs();
    var inPal = !!pal && !pal.hidden;
    if (inPal) {
      var items = palFiltered();
      if (e.key === "Escape") { closePalette(); e.preventDefault(); }
      else if (e.key === "ArrowDown") { palSel = Math.min(palSel + 1, items.length - 1); paintSel(); e.preventDefault(); }
      else if (e.key === "ArrowUp") { palSel = Math.max(palSel - 1, 0); paintSel(); e.preventDefault(); }
      else if (e.key === "Enter") { var it = items[palSel]; if (it) location.href = it.u; e.preventDefault(); }
      return;
    }
    if ((e.metaKey || e.ctrlKey) && e.key === "k") { e.preventDefault(); KEYBAR["⌘K"](); return; }
    // ⌘⏎ submits the composer you're typing in — send, publish, invoke, put.
    // Works from plain textareas and from inside CodeMirror editors alike.
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      var form = document.activeElement.closest ? document.activeElement.closest("form") : null;
      if (form && form.querySelector("textarea, .CodeMirror")) { e.preventDefault(); form.requestSubmit(); return; }
      // And on a create page, from any field or none: the page is for making one
      // thing, and the key bar says ⌘⏎ makes it (board Create). A dialog on top
      // has the key first.
      if (!overlayOpen() && submitCreate()) { e.preventDefault(); return; }
    }
    // Esc in the filter clears it and hands the keys back to the list.
    // At 1100 and below Escape shuts the switcher's menu first, wherever the
    // focus is in it, and gives the page back.
    if (e.key === "Escape" && !overlayOpen() && closeSwitcher(true)) { e.preventDefault(); return; }
    // The menu's filter takes the arrows and Enter as well: type, then Enter
    // opens the first row drawn, or the one under the cursor.
    if (document.activeElement.matches && document.activeElement.matches(".listpane.lp-open .filter input")) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") { moveCursor(e.key === "ArrowDown" ? 1 : -1); e.preventDefault(); return; }
      if (e.key === "Enter") {
        if (cursor < 0) moveCursor(1);
        openCursorRow();
        e.preventDefault();
        return;
      }
    }
    if (e.key === "Escape" && document.activeElement.matches && document.activeElement.matches(".listpane .filter input")) {
      var fi = document.activeElement;
      clearFilter(fi);
      fi.blur();
      e.preventDefault();
      return;
    }
    // Escape works from a field: a create page is cancelled from the name box it
    // opens on, and an inspector closes wherever the focus is. A dialog or a
    // drawer on top takes it first (each closes itself on Escape).
    if (e.key === "Escape") {
      if (!e.defaultPrevented && !overlayOpen() && KEYBAR.esc()) e.preventDefault();
      return;
    }
    var tag = document.activeElement.tagName;
    if (/(INPUT|TEXTAREA|SELECT)/.test(tag) || document.activeElement.closest(".CodeMirror")) return;
    // A modified key is the browser's or the system's (⌘C is not "c").
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    // Enter on a focused button or link presses it; it does not also open a row.
    if (e.key === "Enter" && /^(A|BUTTON|SUMMARY)$/.test(tag)) return;
    if (e.key === "[") { setRail(!railSlim()); return; }
    var cap = KEYCAP[e.key];
    if (cap && KEYBAR[cap]() ) e.preventDefault();
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
        // outerMorph, which under htmx 4 is a built-in swap style rather than
        // an extension that has to be activated. That distinction is the whole
        // history of this line: morphing here never once ran. hx-ext was
        // absent for the life of the feature so "morph" fell back to innerHTML
        // and nested each region inside itself on first change; activating the
        // extension on <body> then broke UNRELATED swaps, because idiomorph
        // claims every style it does not recognise as inline. The retreat to a
        // plain outerHTML replace was the right call at the time and cost
        // scroll position inside a region on every tick that changed anything.
        //
        // Nothing to activate now, so the feature can finally do what it was
        // written for. The 204 no-change path still swaps nothing at all,
        // which is most ticks.
        var swap = cur.getAttribute("data-live-swap") || "outerMorph";
        var hash = cur.getAttribute("data-hash") || "";
        // select:"" is load-bearing, and it is the JS half of the 198
        // hx-select="" attributes in the templates. htmx 4's ajax() resolves
        // the source element as `sourceElt ||= target`, so the source here is
        // the live region — which sits inside #workspace and therefore
        // INHERITS its hx-select:inherited="#workspace". The region's partial
        // has no #workspace in it, so every poll would select nothing and swap
        // nothing, silently, forever. htmx 2 sourced htmx.ajax() from
        // document.body and never picked the attribute up.
        htmx.ajax("GET", url + (url.indexOf("?") >= 0 ? "&" : "?") + "h=" + hash, {
          target: "#" + id, swap: swap, select: "",
        });
      }, every);
      liveTimers.push(t);
    });
  }
  // A live region is morphed in place, and the server's markup knows nothing of
  // what Alpine has done to the copy on screen: x-show's inline display:none,
  // the class a :class binding added. Morphing those attributes back to the
  // server's made every closed message row open on the first refresh (the
  // style went, Alpine's state did not change, so nothing put it back), and
  // closed an open one the same way. Alpine owns those attributes on the
  // elements it is bound to, so the morph leaves them alone there.
  //
  // before:morph:attr is one of htmx 4's extension hooks, not a DOM event: it
  // is called with the element and {attrName, newValue}, and returning false
  // skips that one attribute.
  if (window.htmx && htmx.registerExtension) {
    htmx.registerExtension("doze-alpine-morph", {
      htmx_before_morph_attr: function (el, detail) {
        var name = detail && detail.attrName;
        if (!el || !el.hasAttribute || !name) return true;
        if (name === "x-cloak") return !(el._x_dataStack || (el.closest && el.closest("[x-data]")));
        if (name === "style" && el.hasAttribute("x-show")) return false;
        if (name === "class" && (el.hasAttribute(":class") || el.hasAttribute("x-bind:class"))) return false;
        return true;
      },
    });
  }
  document.addEventListener("DOMContentLoaded", setupLive);
  document.addEventListener("htmx:after:swap", function (e) {
    // Re-arm after a navigation OR after a swap that lands (or contains) a live
    // region — including one whose target IS the data-live element itself, which
    // a descendant-only querySelector check would miss.
    var t = e.detail.ctx && e.detail.ctx.target;
    if (t && (t.id === "workspace" ||
              (t.matches && t.matches("[data-live]")) ||
              (t.querySelector && t.querySelector("[data-live]")))) setupLive();
  });
  // After each poll, stamp the element's data-hash from the response header — a
  // morph swap doesn't reliably update the root's attributes, so without this the
  // poll would keep re-fetching the same change every tick.
  document.addEventListener("htmx:after:request", function (e) {
    var ctx = e.detail.ctx;
    if (!ctx || !ctx.response) return;
    var h = ctx.response.headers.get("HX-Live-Hash");
    if (!h) return;
    // Path AND query, not just the path: a data-live base can carry a filter
    // (#sfn-executions is ".../executions?status=RUNNING"), and the prefix
    // match below is against that whole string. Dropping the query makes every
    // filtered live region stop stamping its hash, which strands the poll on a
    // stale hash and it never sees another change.
    var action = (ctx.request && ctx.request.action) || "";
    var path = action;
    try { var u = new URL(action, location.href); path = u.pathname + u.search; } catch (err) {}
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
  // data-sleep-fmt="clock" writes m:ss (the held-message table's column), and
  // a .rt-bar inside the same host is drawn as the share of data-sleep-total
  // that is left.
  function fmtClock(s) { return Math.floor(s / 60) + ":" + String(s % 60).padStart(2, "0"); }
  function tickCountdowns() {
    document.querySelectorAll(".rt-cd").forEach(function (cd) {
      var host = cd.closest("[data-sleep-left]");
      if (!host) return;
      // A morph keeps the element and changes the attribute: the deadline is
      // re-based whenever the server's number is a new one.
      var stated = host.getAttribute("data-sleep-left") || "0";
      if (host._deadline == null || host._stated !== stated) {
        host._stated = stated;
        host._deadline = Date.now() + parseInt(stated, 10) * 1000;
      }
      var left = Math.round((host._deadline - Date.now()) / 1000);
      // The expired wording belongs to the caller. "any moment" is right for
      // a function about to go cold, and wrong for a message whose visibility
      // timeout has lapsed, which is "visible again" — same timer, different
      // fact.
      var clock = host.getAttribute("data-sleep-fmt") === "clock";
      cd.textContent = left > 0 ? (clock ? fmtClock(left) : fmtLeft(left))
        : (host.getAttribute("data-sleep-done") || "any moment");
      var barEl = host.querySelector(".rt-bar");
      if (barEl) {
        var total = parseInt(host.getAttribute("data-sleep-total") || "0", 10);
        barEl.style.width = (total > 0 ? Math.max(0, Math.min(100, Math.round(left / total * 100))) : 0) + "%";
      }
    });
  }
  setInterval(tickCountdowns, 1000);
  // And at once when one arrives, so a row is never drawn with the number
  // missing for its first second.
  document.addEventListener("DOMContentLoaded", tickCountdowns);
  document.addEventListener("htmx:after:swap", function () { setTimeout(tickCountdowns, 0); });

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
  document.addEventListener("htmx:before:request", function (e) {
    if (String((e.detail.ctx && e.detail.ctx.request || {}).method).toUpperCase() === "GET") return;
    reqDepth++;
    if (reqTimer) return;
    reqTimer = setTimeout(function () { bar().classList.add("on"); }, 300);
  });
  document.addEventListener("htmx:after:request", function (e) {
    if (String((e.detail.ctx && e.detail.ctx.request || {}).method).toUpperCase() === "GET") return;
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
  // checks htmx's own processed marker and rescues only elements htmx has
  // genuinely not seen. htmx 2 kept that marker in an "htmx-internal-data"
  // property with an initHash; htmx 4 keeps it on elt._htmx and records the
  // fact of initialization directly, which is what its own #shouldProcess
  // tests. Reading the current one matters: a marker probe that never matches
  // silently re-processes every bound element on every click.
  document.addEventListener("click", function (e) {
    if (!window.htmx || !e.target || !e.target.closest) return;
    var c = e.target.closest("[hx-post],[hx-get],[hx-put],[hx-patch],[hx-delete]");
    if (!c || c.tagName === "FORM") return;
    if (c.form || c.closest("form")) return; // form members ride the submit guard
    if (c._htmx && c._htmx.initialized) return; // bound: htmx's own listener already fired
    htmx.process(c);
    htmx.trigger(c, "click");
  });

  // ---------- double-submit guard ----------
  // htmx:before:request is safe to disable controls in. Under htmx 2 it was
  // not — the payload was serialized between beforeRequest and beforeSend, so
  // a control disabled at beforeRequest dropped out of the body and this had
  // to listen on beforeSend instead. htmx 4 builds ctx.request.body before it
  // fires htmx:config:request, which is earlier still, so by the time
  // before:request runs the body is already committed.
  //
  // hx-disable="this" (htmx 2's hx-disabled-elt) is not usable here — it is
  // inheritable, but "this" resolves to the ancestor CARRYING the attribute,
  // not to the actuator, so a body-level declaration would disable the wrapper
  // and leave the button live.
  function submitControls(elt) {
    if (!elt || !elt.tagName) return [];
    if (elt.tagName === "BUTTON" || elt.tagName === "INPUT") return [elt];
    return Array.prototype.slice.call(
      elt.querySelectorAll('button[type="submit"], button:not([type]), input[type="submit"]'));
  }
  document.addEventListener("htmx:before:request", function (e) {
    var ctx = e.detail.ctx;
    if (!ctx || String(ctx.request.method).toUpperCase() === "GET") return;
    var elt = ctx.sourceElement;
    var ctrls = submitControls(elt).filter(function (c) { return !c.disabled; });
    if (!ctrls.length) return;
    ctrls.forEach(function (c) { c.disabled = true; });
    elt._dozeLocked = ctrls;
  });
  function unlock(e) {
    var ctx = e.detail && e.detail.ctx;
    var elt = ctx && ctx.sourceElement;
    if (!elt || !elt._dozeLocked) return;
    // Guard against a swap having replaced the button underneath us.
    elt._dozeLocked.forEach(function (c) { if (document.contains(c)) c.disabled = false; });
    elt._dozeLocked = null;
  }
  // htmx:error is htmx 4's single failure event: it replaces sendError, timeout,
  // swapError and targetError, which each needed their own listener before.
  document.addEventListener("htmx:after:request", unlock);
  document.addEventListener("htmx:error", unlock);

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
  document.addEventListener("htmx:after:swap", watchPanes);
  watchPanes();

  // ---------- after an in-place navigation ----------
  // c.redirect now swaps #workspace instead of reloading the window. Five things
  // a full page load used to do for free have to be done here.
  var lastPath = location.pathname;
  function serviceOf(p) {
    var seg = p.replace(PREFIX, "").split("/").filter(Boolean);
    return seg.length ? seg[0] : "";
  }
  document.addEventListener("htmx:after:settle", function (e) {
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
  document.addEventListener("htmx:after:settle", tidyFlash);

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
  // The last flash painted and when. The 120ms timer below can paint before
  // a slow follow-up GET swaps #workspace, and the swap destroys the banner
  // with nothing left to paint; afterSettle re-paints a banner that was
  // painted moments ago and is no longer in the document.
  var recentFlash = null;
  function paintFlash() {
    var rescued = false;
    if (!pendingFlash && recentFlash && Date.now() - recentFlash.at < 3000 && !document.getElementById("flashbar")) {
      pendingFlash = recentFlash.f;
      rescued = true;
    }
    if (!pendingFlash) return;
    var f = pendingFlash;
    pendingFlash = null;
    // One rescue, never a second: the swap that destroys the banner happens
    // once, and a later settle is some other navigation.
    recentFlash = rescued ? null : { f: f, at: Date.now() };
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
    // A success is a toast, which lives outside #workspace and so survives the
    // swap a redirect is about to make. Only a value you must copy before it
    // leaves (a secret) stays a banner.
    if (!sticky) { toast(msg, "ok"); return; }
    pendingFlash = { msg: msg, sticky: sticky };
    setTimeout(paintFlash, 120); // no navigation coming — paint it anyway
  }
  window.addEventListener("doze:flash", function (e) { queueFlash(e, false); });
  // A value that cannot be retrieved again also gets a copy button.
  window.addEventListener("doze:flash-sticky", function (e) { queueFlash(e, true); });
  document.addEventListener("htmx:after:settle", paintFlash);
  // The follow-up GET a redirect issues is not the user's doing; anything
  // they click or type after the banner is. A tab clicked a second after
  // "Stream deleted" used to bring the message back as if it were new.
  function disarmFlash() { recentFlash = null; }
  document.addEventListener("pointerdown", disarmFlash, true);
  document.addEventListener("keydown", disarmFlash, true);

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
  // ---------- scrollable regions ----------
  // A code block, a log pane or a wide table that scrolls has to be reachable
  // from the keyboard, or its overflow can only be read with a mouse wheel.
  // rootOf: an htmx event's target can be the document's window or a node with
  // no querySelectorAll; then the whole document is what to look through.
  function rootOf(root) { return root && root.querySelectorAll ? root : document; }
  function focusableScrollers(root) {
    rootOf(root).querySelectorAll(".code-out pre, pre, .log-lines, .table-wrap").forEach(function (el) {
      if (el.hasAttribute("tabindex")) return;
      if (el.scrollHeight > el.clientHeight + 1 || el.scrollWidth > el.clientWidth + 1) el.setAttribute("tabindex", "0");
    });
  }
  document.addEventListener("DOMContentLoaded", function () { focusableScrollers(); });
  document.addEventListener("htmx:after:settle", function (e) { focusableScrollers(e.target); });
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
    // Focus that is somewhere else (the dialog was not shown yet when it
    // opened, or a click put it on the page behind) comes in, not along.
    if (!trapped.contains(document.activeElement)) { e.preventDefault(); (e.shiftKey ? last : first).focus(); }
    else if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
    else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
  }
  // Alpine's x-show reveals on a timer of its own, so a dialog opened by a
  // state change is still display:none when this is called and has nothing to
  // focus: the redrive dialog opened with focus left on the button behind it.
  // Try for a few frames until something in it can take focus.
  function focusInto(el, tries) {
    if (trapped !== el) return;
    var f = focusables(el);
    if (f.length) { f[0].focus(); return; }
    if (tries > 0) requestAnimationFrame(function () { focusInto(el, tries - 1); });
  }
  window.dozeTrap = function (el, open) {
    if (open) {
      if (trapped === el) return;
      trapReturn = document.activeElement;
      trapped = el;
      document.addEventListener("keydown", onTrapKey, true);
      setTimeout(function () { focusInto(el, 20); }, 0);
    } else if (trapped === el) {
      trapped = null;
      document.removeEventListener("keydown", onTrapKey, true);
      var back = trapReturn;
      trapReturn = null;
      // What opened it may have been replaced by the swap the dialog caused
      // (a panel re-rendered with the same button): a control that says where
      // to find its successor (data-focus-key) gets focus back by that.
      if (back && !document.contains(back) && back.getAttribute && back.getAttribute("data-focus-key")) {
        back = document.querySelector('[data-focus-key="' + back.getAttribute("data-focus-key") + '"]');
      }
      if (back && document.contains(back)) setTimeout(function () { back.focus(); }, 0);
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
    rootOf(root).querySelectorAll(".field").forEach(function (f) {
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
  document.addEventListener("htmx:after:swap", function (e) { linkLabels(e.target); });
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

  // ---------- the SQS composer and create form ----------
  // Everything here is delegated from the document and reads the form as it
  // stands. None of it is called from an Alpine expression: this file is
  // deferred AFTER Alpine, so on a full page load Alpine has evaluated every
  // expression on the page before anything defined here exists.
  //
  // The preview is built from the fields the form posts and by the rules the
  // handler applies (sqsSend, SendMessage, SendMessageBatch), so the command
  // shown is the call made:
  //   - a delay of 0 or blank is not sent;
  //   - attribute rows with no name are dropped, a Binary one goes as BinaryValue;
  //   - a FIFO send with no deduplication id typed carries the id shown here
  //     (dedup_auto, renewed after every send), unless the queue deduplicates
  //     on content, when none is sent;
  //   - more than one copy is ONE SendMessageBatch of at most ten entries,
  //     ids m0..m9, each FIFO entry with the id and "-<n>".
  function sqsNewId() {
    return "console-" + Date.now().toString(36) + Math.random().toString(36).slice(2, 8);
  }
  function shQuote(v) {
    return /^[A-Za-z0-9_.:\/@,+][A-Za-z0-9_.:\/@=,+-]*$/.test(v) ? v : "'" + String(v).replace(/'/g, "'\\''") + "'";
  }
  function sqsAuto(form) {
    var a = form.elements["dedup_auto"];
    if (a && !a.value) a.value = sqsNewId();
    return a ? a.value : "";
  }
  function sqsCli(form) {
    var val = function (name) {
      var el = form.elements[name];
      if (!el) return "";
      return el.__cm ? el.__cm.getValue() : el.value;
    };
    var fifo = form.getAttribute("data-fifo") === "true", contentDedup = form.getAttribute("data-content-dedup") === "true";
    var attrs = null;
    try {
      JSON.parse(val("attrs") || "[]").forEach(function (r) {
        var name = (r.n || "").trim();
        if (!name) return;
        attrs = attrs || {};
        var t = r.t || "String";
        attrs[name] = t === "Binary" ? { DataType: t, BinaryValue: r.v } : { DataType: t, StringValue: r.v };
      });
    } catch (e) {}
    var body = val("body"), delay = val("delay").trim();
    var group = fifo ? (val("group").trim() || "default") : "";
    var typed = fifo ? val("dedup").trim() : "", auto = sqsAuto(form);
    var copies = Math.max(1, Math.min(parseInt(val("repeat"), 10) || 1, 10));
    var url = form.getAttribute("data-url") || "";
    var head = "aws --endpoint-url http://" + location.host + " sqs ";
    if (copies > 1) {
      var base = typed || auto, entries = [];
      for (var i = 0; i < copies; i++) {
        var e = { Id: "m" + i, MessageBody: body };
        if (delay && delay !== "0") e.DelaySeconds = parseInt(delay, 10) || 0;
        if (fifo) { e.MessageGroupId = group; e.MessageDeduplicationId = base + "-" + i; }
        if (attrs) e.MessageAttributes = attrs;
        entries.push(e);
      }
      return head + "send-message-batch --queue-url " + shQuote(url) + " --entries " + shQuote(JSON.stringify(entries));
    }
    var cmd = head + "send-message --queue-url " + shQuote(url) + " --message-body " + shQuote(body);
    if (delay && delay !== "0") cmd += " --delay-seconds " + shQuote(delay);
    if (fifo) {
      cmd += " --message-group-id " + shQuote(group);
      var dedup = typed || (contentDedup ? "" : auto);
      if (dedup) cmd += " --message-deduplication-id " + shQuote(dedup);
    }
    if (attrs) cmd += " --message-attributes " + shQuote(JSON.stringify(attrs));
    return cmd;
  }
  // Alpine writes the attribute rows into the hidden field after the event
  // that changed them, so the preview is redrawn a tick later.
  function sqsRefresh(form) {
    setTimeout(function () {
      var pre = form.querySelector("[data-sqs-cli]");
      if (pre) pre.textContent = sqsCli(form);
    }, 0);
  }
  ["input", "change", "click"].forEach(function (ev) {
    document.addEventListener(ev, function (e) {
      var form = e.target && e.target.closest && e.target.closest("form[data-sqs-send]");
      if (form) sqsRefresh(form);
    });
  });
  // The id is in the form before the form is sent, whether or not the
  // preview was ever opened.
  document.addEventListener("submit", function (e) {
    var form = e.target && e.target.closest && e.target.closest("form[data-sqs-send]");
    if (form) sqsAuto(form);
  }, true);
  document.addEventListener("htmx:config:request", function (e) {
    var src = e.detail && e.detail.ctx && e.detail.ctx.sourceElement;
    var form = src && src.closest && src.closest("form[data-sqs-send]");
    if (!form) return;
    var body = e.detail.ctx.request && e.detail.ctx.request.body;
    if (body && body.get && body.set && !body.get("dedup_auto")) body.set("dedup_auto", sqsAuto(form));
  });
  // A send that went through used up the id the preview showed, so the next
  // one gets a new one; and the message it made is the last row of the list.
  document.addEventListener("htmx:after:request", function (e) {
    var ctx = e.detail && e.detail.ctx;
    var form = ctx && ctx.sourceElement && ctx.sourceElement.closest && ctx.sourceElement.closest("form[data-sqs-send]");
    if (!form || !ctx.response || ctx.response.status >= 400) return;
    var a = form.elements["dedup_auto"];
    if (a) a.value = sqsNewId();
    sqsRefresh(form);
    requestAnimationFrame(function () {
      var m = document.querySelectorAll("#message-panel-wrap .msg");
      if (m.length) m[m.length - 1].classList.add("landed");
    });
  });
  // The SQS create form has two layouts (boards Sqs-03 and Sqs-04): Advanced
  // is the last, closed card of the form's own column on a standard queue, and
  // the first, open card of the right-hand column on a FIFO queue, with the
  // buttons under whichever column ends the form. One form, one Advanced: the
  // nodes are moved, not drawn twice.
  function sqsCreateLayout(form, fifo) {
    var left = form.querySelector("[data-col=left]"), right = form.querySelector("[data-col=right]");
    var adv = form.querySelector(".adv-sec"), acts = form.querySelector(".q-actions"), group = form.querySelector(".q-group");
    if (!left || !right || !adv || !acts) return;
    if (fifo) {
      if (adv.parentNode !== right) { right.insertBefore(adv, group || null); adv.open = true; }
      if (acts.parentNode !== right) right.appendChild(acts);
    } else {
      if (adv.parentNode !== left) { left.appendChild(adv); adv.open = false; }
      left.appendChild(acts);
    }
    if (window.dozeEditor && adv.open) dozeEditor.refresh(adv);
  }
  document.addEventListener("change", function (e) {
    var t = e.target;
    if (!t || !t.matches || !t.matches(".q-create input[name=fifo]")) return;
    sqsCreateLayout(t.form, t.checked);
  });
  window.dozeShell = { toast: toast, openPalette: openPalette, clearFilter: clearFilter };
})();

// ---------- where a result lands ----------
// Detail and output panels are filled by a button somewhere else on the page, so
// the result can open below the fold with nothing to say it did. After a swap
// into one, bring it into view, and give a detail panel a way to close it.
// Scoped to the ids the console gives such panels (…-detail, …-out, …-result)
// so nothing that is polled or morphed in place is touched.
(function () {
  var PANEL = /-(detail|out|result)$/;
  document.addEventListener("htmx:after:swap", function (e) {
    var ctx = e.detail && e.detail.ctx;
    var t = ctx && ctx.target;
    if (!t || !t.id || !PANEL.test(t.id)) return;
    var host = document.getElementById(t.id); // an outerHTML swap leaves ctx.target detached
    if (!host || !host.textContent.trim()) return;
    if (/-detail$/.test(host.id) && !host.querySelector(":scope > .dz-close")) {
      var x = document.createElement("button");
      x.type = "button";
      x.className = "icon-btn sm dz-close";
      x.setAttribute("aria-label", "Close");
      x.title = "Close";
      x.textContent = "×";
      x.addEventListener("click", function () { host.innerHTML = ""; });
      host.insertBefore(x, host.firstChild);
    }
    var r = host.getBoundingClientRect();
    if (r.top >= 0 && r.bottom <= window.innerHeight) return; // already in view
    var calm = window.matchMedia && matchMedia("(prefers-reduced-motion: reduce)").matches;
    host.scrollIntoView({ block: "nearest", behavior: calm ? "auto" : "smooth" });
  });
})();
