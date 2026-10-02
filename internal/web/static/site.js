// DoesItOmarchy: progressive enhancement only. Every feature below has a
// no-JS fallback (plain links and forms), and nothing here uses eval.
(function () {
  "use strict";
  var root = document.documentElement;
  root.classList.add("js"); // shows controls that need script ([data-js])

  // ── theme picker ──────────────────────────────────────────────────────
  var picker = document.getElementById("theme");
  if (picker) {
    picker.hidden = false;
    picker.value = root.getAttribute("data-theme") || "system";
    picker.addEventListener("change", function () {
      var t = picker.value;
      if (t === "system") root.removeAttribute("data-theme");
      else root.setAttribute("data-theme", t);
      try { localStorage.setItem("theme", t); } catch (e) {}
    });
  }

  // ── search overlay (Ctrl+K, ⌘K or /) ─────────────────────────────────
  var overlay = document.getElementById("overlay");
  function openOverlay() {
    if (!overlay || !overlay.showModal) return false;
    if (!overlay.open) overlay.showModal();
    var q = overlay.querySelector("input[name=q]");
    q.focus();
    q.select();
    return true;
  }
  document.addEventListener("keydown", function (e) {
    var typing = /^(INPUT|TEXTAREA|SELECT)$/.test(e.target.tagName) || e.target.isContentEditable;
    if ((e.key === "k" && (e.ctrlKey || e.metaKey)) || (e.key === "/" && !typing)) {
      if (openOverlay()) e.preventDefault();
    }
  });
  document.querySelectorAll("[data-open-search]").forEach(function (a) {
    a.addEventListener("click", function (e) { if (openOverlay()) e.preventDefault(); });
  });
  if (overlay) {
    overlay.addEventListener("click", function (e) { if (e.target === overlay) overlay.close(); });
  }

  // ── suggestions and keyboard navigation for every search box ─────────
  document.querySelectorAll("input[data-suggest]").forEach(function (input) {
    var list = document.querySelector(input.getAttribute("data-suggest"));
    var scope = input.closest("[data-search-scope]") || document;
    var timer = null, seq = 0;

    function fetchSuggest() {
      var n = ++seq;
      var url = "/search/suggest?q=" + encodeURIComponent(input.value) + "&cursor=" + (input.selectionStart || 0);
      fetch(url, { headers: { "HX-Request": "true" } }).then(function (r) { return r.text(); }).then(function (html) {
        if (n !== seq) return;
        var tmp = document.createElement("div");
        tmp.innerHTML = html;
        var ul = tmp.querySelector("ul");
        list.innerHTML = ul ? ul.innerHTML : "";
      }).catch(function () {});
    }
    input.addEventListener("input", function () {
      clearTimeout(timer);
      timer = setTimeout(fetchSuggest, 120);
    });
    input.addEventListener("focus", function () { if (input.value) fetchSuggest(); });

    function apply(a) {
      input.value = a.getAttribute("data-query");
      var c = parseInt(a.getAttribute("data-cursor"), 10);
      input.focus();
      input.setSelectionRange(c, c);
      input.dispatchEvent(new Event("input", { bubbles: true })); // refresh results (htmx) and suggestions
    }
    list.addEventListener("click", function (e) {
      var a = e.target.closest("a[data-query]");
      if (a) { e.preventDefault(); apply(a); }
    });

    function items() { return Array.prototype.slice.call(scope.querySelectorAll(".suggest a[data-query], .results a.hit")); }
    function current(all) { return all.findIndex(function (a) { return a.classList.contains("sel"); }); }
    function select(all, i) {
      all.forEach(function (a) { a.classList.remove("sel"); });
      if (i >= 0 && all[i]) { all[i].classList.add("sel"); all[i].scrollIntoView({ block: "nearest" }); }
    }
    input.addEventListener("keydown", function (e) {
      var all = items(), i = current(all);
      if (e.key === "ArrowDown") { e.preventDefault(); select(all, Math.min(i + 1, all.length - 1)); }
      else if (e.key === "ArrowUp") { e.preventDefault(); select(all, Math.max(i - 1, -1)); }
      else if (e.key === "Enter" && i >= 0) {
        e.preventDefault();
        if (all[i].hasAttribute("data-query")) apply(all[i]); else window.location = all[i].href;
      } else if (e.key === "Tab" && !e.shiftKey) {
        var first = list.querySelector("a[data-query]");
        if (first && input.value && list.children.length) { e.preventDefault(); apply(first); }
      }
    });
  });

  // ── criteria matrices: highlight the hovered column ───────────────────
  document.querySelectorAll("table.matrix").forEach(function (table) {
    var heads = table.tHead ? table.tHead.rows[0].cells : [];
    function rows() { return Array.prototype.filter.call(table.tBodies[0].rows, function (r) { return r.cells.length === heads.length; }); }
    function mark(cls, i) {
      table.querySelectorAll("td." + cls).forEach(function (td) { td.classList.remove(cls); });
      if (i > 0 && i < heads.length - 1) rows().forEach(function (r) { r.cells[i].classList.add(cls); });
    }
    table.addEventListener("mouseover", function (e) {
      var cell = e.target.closest("td, th");
      mark("col-hover", cell && cell.parentElement.cells.length === heads.length ? cell.cellIndex : -1);
    });
    table.addEventListener("mouseleave", function () { mark("col-hover", -1); });
    function target() {
      var id = location.hash.slice(1);
      var i = Array.prototype.findIndex.call(heads, function (h) { return h.id === id; });
      mark("col-target", i);
    }
    window.addEventListener("hashchange", target);
    target();
  });

  // ── easter egg: ↑ ↑ ↓ ↓ ← → ← → B A Enter ─────────────────────────────
  var code = ["ArrowUp", "ArrowUp", "ArrowDown", "ArrowDown", "ArrowLeft", "ArrowRight", "ArrowLeft", "ArrowRight", "b", "a", "Enter"];
  var pos = 0, active = false;
  var konami = document.getElementById("konami");
  var calm = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  // An original 8-bit "power-up": rising square-wave arpeggios, synthesised
  // here (no audio file, nothing third-party).
  function powerUp() {
    var AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return;
    try {
      var ac = new AC(), t = ac.currentTime + 0.02, gain = ac.createGain();
      gain.gain.value = 0.06;
      gain.connect(ac.destination);
      var notes = [60, 64, 67, 72, 62, 66, 69, 74, 64, 68, 71, 76, 79, 84];
      notes.forEach(function (n, i) {
        var o = ac.createOscillator(), g = ac.createGain(), at = t + i * 0.055, len = i === notes.length - 1 ? 0.32 : 0.05;
        o.type = "square";
        o.frequency.value = 440 * Math.pow(2, (n - 69) / 12);
        g.gain.setValueAtTime(1, at);
        g.gain.exponentialRampToValueAtTime(0.001, at + len);
        o.connect(g); g.connect(gain);
        o.start(at); o.stop(at + len + 0.02);
      });
      setTimeout(function () { ac.close(); }, 1500);
    } catch (e) {}
  }

  var ease = function (x) { return x < 0.5 ? 4 * x * x * x : 1 - Math.pow(-2 * x + 2, 3) / 2; };

  // tween runs fn(progress 0→1) over ms with easing, then done.
  function tween(ms, fn, done) {
    if (calm || ms <= 0) { fn(1); if (done) done(); return; }
    var t0 = performance.now();
    requestAnimationFrame(function step(now) {
      var x = Math.min(1, (now - t0) / ms);
      fn(ease(x));
      if (x < 1) requestAnimationFrame(step); else if (done) done();
    });
  }

  function scrollToY(y, ms, done) {
    var from = window.scrollY;
    tween(Math.abs(y - from) < 2 ? 0 : ms, function (k) { window.scrollTo({ top: from + (y - from) * k, behavior: "instant" }); }, done);
  }

  function celebrate() {
    active = true;
    var body = document.body, startY = window.scrollY, armed = false, timers = [];
    var lit = document.querySelector(".super .c-cov"), footer = document.querySelector(".super");
    var msg = konami.querySelector(".konami-msg");
    var later = function (ms, fn) { timers.push(setTimeout(fn, ms)); };

    // Nothing interrupts the sequence until it has played; after that, any input clears it.
    function input(e) {
      if (!armed) {
        if (e.type !== "mousemove") { e.preventDefault(); e.stopPropagation(); }
        return;
      }
      reset();
    }
    var events = ["keydown", "mousemove", "mousedown", "touchstart", "touchmove", "wheel"];
    events.forEach(function (ev) { addEventListener(ev, input, { capture: true, passive: false }); });

    // 1. Down to the very bottom, so the whole footer is in view.
    scrollToY(document.documentElement.scrollHeight - window.innerHeight, 700, function () {
      // 2. Dim everything but the footer's coverage column.
      body.classList.add("konami-lit");
      konami.hidden = false;
      void konami.offsetWidth;
      konami.classList.add("on");
      later(calm ? 0 : 450, function () {
        // 3. The message, in the dimmed space above the footer.
        var room = footer.getBoundingClientRect().top;
        if (room < 120 && lit) room = lit.getBoundingClientRect().top - 16;
        msg.style.height = Math.max(room, 80) + "px";
        msg.style.setProperty("--fit", "1");
        var box = msg.firstElementChild, w = msg.clientWidth - 32;
        var fit = Math.min(1, room / (box.scrollHeight + 32), w / box.scrollWidth);
        msg.style.setProperty("--fit", String(Math.max(0.45, fit)));
        konami.classList.add("msg");
        fill();
        later(calm ? 0 : 2900, function () { armed = true; });
      });
    });

    // 4. Every coverage bar climbs to 100%, the figures counting along.
    var saved = [];
    function fill() {
      document.querySelectorAll(".stat").forEach(function (st) {
        var rect = st.querySelector("svg.bar rect"), fig = st.querySelector(".figure");
        var n = fig.querySelector(".n"), p = fig.querySelector(".p"), total = +fig.getAttribute("data-total");
        var from = +n.textContent, w0 = parseFloat(rect.getAttribute("width")) || 0;
        saved.push([rect, n, n.textContent, p, p.textContent]);
        tween(2600, function (k) {
          rect.style.width = (w0 + (100 - w0) * k) + "px";
          n.textContent = Math.round(from + (total - from) * k);
          p.textContent = (100 * (from + (total - from) * k) / (total || 1)).toFixed(1) + "%";
        });
      });
    }

    function reset() {
      if (!active) return;
      active = false;
      timers.forEach(clearTimeout);
      events.forEach(function (ev) { removeEventListener(ev, input, { capture: true, passive: false }); });
      konami.classList.remove("on", "msg");
      setTimeout(function () {
        konami.hidden = true;
        body.classList.remove("konami-lit");
        saved.forEach(function (s) { s[0].style.width = ""; s[1].textContent = s[2]; s[3].textContent = s[4]; });
        scrollToY(startY, 500);
      }, calm ? 0 : 400);
    }
    later(15000, function () { armed = true; reset(); });
  }

  function flashThenCelebrate() {
    powerUp();
    if (calm) { celebrate(); return; } // no flashing for visitors who ask for reduced motion
    // Two flashes ~400ms apart: well under WCAG's three-flashes-per-second limit.
    var body = document.body, steps = [[0, true], [180, false], [400, true], [580, false]];
    steps.forEach(function (s) { setTimeout(function () { body.classList.toggle("flash", s[1]); }, s[0]); });
    setTimeout(celebrate, 600);
  }

  if (konami) {
    document.addEventListener("keydown", function (e) {
      if (active) return;
      var typing = /^(INPUT|TEXTAREA|SELECT)$/.test(e.target.tagName) || e.target.isContentEditable || (overlay && overlay.open);
      if (typing) { pos = 0; return; }
      var key = e.key.length === 1 ? e.key.toLowerCase() : e.key;
      pos = key === code[pos] ? pos + 1 : (key === code[0] ? 1 : 0);
      if (pos === code.length) { pos = 0; e.preventDefault(); flashThenCelebrate(); }
    });
  }
  // ── copy to clipboard ─────────────────────────────────────────────────
  // The Clipboard API exists only on secure origins (HTTPS, localhost), and
  // some browsers refuse it anyway, so fall back to a hidden textarea and
  // execCommand("copy"), which works on plain HTTP too.
  function legacyCopy(text) {
    var ta = document.createElement("textarea");
    ta.value = text;
    ta.setAttribute("readonly", "");
    ta.className = "clip-buffer";
    document.body.appendChild(ta);
    ta.select();
    ta.setSelectionRange(0, text.length);
    var ok = false;
    try { ok = document.execCommand("copy"); } catch (e) {}
    document.body.removeChild(ta);
    return ok;
  }
  function copyText(text) {
    if (navigator.clipboard && window.isSecureContext) {
      return navigator.clipboard.writeText(text).catch(function () {
        if (!legacyCopy(text)) throw new Error("copy refused");
      });
    }
    return legacyCopy(text) ? Promise.resolve() : Promise.reject(new Error("copy refused"));
  }
  function selectText(el) {
    var r = document.createRange();
    r.selectNodeContents(el);
    var sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(r);
  }
  document.addEventListener("click", function (e) {
    var b = e.target.closest && e.target.closest("[data-copy]");
    if (!b) return;
    var code = document.getElementById(b.getAttribute("data-copy"));
    var status = document.getElementById("copy-status");
    copyText(code.textContent).then(function () {
      b.classList.add("done");
      if (status) status.textContent = "Copied to the clipboard.";
      setTimeout(function () { b.classList.remove("done"); }, 2000);
    }, function () {
      selectText(code); // the visitor can still press Ctrl+C
      if (status) status.textContent = "Couldn't copy automatically; the command is selected, press Ctrl+C.";
    });
  });

  // ── configuration cards: expand or collapse every criteria group ──────
  // Delegated, so it keeps working after HTMX swaps the cards view.
  function syncCapsToggle(btn) {
    var groups = btn.closest(".card").querySelectorAll(".caps > details");
    var allOpen = Array.prototype.every.call(groups, function (d) { return d.open; });
    btn.setAttribute("aria-expanded", String(allOpen));
    btn.title = allOpen ? "Collapse all" : "Expand all";
    btn.setAttribute("aria-label", btn.title);
  }
  document.addEventListener("click", function (e) {
    var btn = e.target.closest && e.target.closest("[data-caps-toggle]");
    if (!btn) return;
    var open = btn.getAttribute("aria-expanded") !== "true";
    btn.closest(".card").querySelectorAll(".caps > details").forEach(function (d) { d.open = open; });
    syncCapsToggle(btn);
  });
  function syncAllCapsToggles() { document.querySelectorAll("[data-caps-toggle]").forEach(syncCapsToggle); }
  syncAllCapsToggles();
  document.addEventListener("htmx:afterSettle", syncAllCapsToggles);
  // A group opened or closed by hand updates its card's button.
  document.addEventListener("toggle", function (e) {
    var card = e.target.closest && e.target.closest(".card");
    var btn = card && card.querySelector("[data-caps-toggle]");
    if (btn && e.target.parentElement && e.target.parentElement.classList.contains("caps")) syncCapsToggle(btn);
  }, true);

  // ── /admin: Reject and Retract reveal their reason box first ─────────
  document.addEventListener("click", function (e) {
    var open = e.target.closest && e.target.closest("[data-reason-open]");
    var cancel = e.target.closest && e.target.closest("[data-reason-cancel]");
    var form = (open || cancel) && (open || cancel).closest("[data-reason-form]");
    if (!form) return;
    form.classList.toggle("open", !!open);
    var opener = form.querySelector("[data-reason-open]");
    opener.setAttribute("aria-expanded", String(!!open));
    if (open) form.querySelector("textarea").focus();
    else opener.focus();
  });

  // ── /identify (PF-1): pick a command, copy it, parse the paste here ───
  // Same rules as internal/match/parse.go. Only the extracted IDs leave the
  // browser, as the result page's URL; the pasted text is never sent.
  var identifyForm = document.getElementById("identify-form");
  if (identifyForm) {
    var osButtons = document.querySelectorAll("[data-os]");
    var showOS = function (os) {
      osButtons.forEach(function (b) { b.setAttribute("aria-pressed", String(b.getAttribute("data-os") === os)); });
      document.querySelectorAll("[data-os-block]").forEach(function (el) { el.hidden = el.getAttribute("data-os-block") !== os; });
    };
    osButtons.forEach(function (b) { b.addEventListener("click", function () { showOS(b.getAttribute("data-os")); }); });
    showOS(/Mac OS X|Macintosh/.test(navigator.userAgent) && !/Linux/.test(navigator.userAgent) ? "macos" : "linux");

    // The clipboard switch: on, the command also copies its output (wl-copy, pbcopy).
    var sw = document.getElementById("clip-switch");
    var step = document.getElementById("paste-step");
    var setClip = function (on) {
      sw.setAttribute("aria-checked", String(on));
      document.querySelectorAll("code[data-clip]").forEach(function (c) { c.textContent = c.getAttribute(on ? "data-clip" : "data-plain"); });
      document.querySelectorAll(".clip-note").forEach(function (n) { n.hidden = !on; });
      step.textContent = step.getAttribute(on ? "data-clip-text" : "data-plain-text");
    };
    sw.addEventListener("click", function () { setClip(sw.getAttribute("aria-checked") !== "true"); });

    identifyForm.addEventListener("submit", function (e) {
      var text = document.getElementById("paste").value;
      var q = new URLSearchParams();
      var id = text.match(/\b(MacBook(?:Air|Pro)?|iMac(?:Pro)?|Macmini|MacPro|Xserve)\d{1,2},\d{1,2}\b/);
      if (id) q.set("product", id[0]);
      var board = text.match(/\bMac-[0-9A-Fa-f]{16}\b/);
      if (board) q.set("board", "Mac-" + board[0].slice(4).toUpperCase());
      var cpu = text.match(/^[ \t]*(?:model name[ \t]*:[ \t]*)?((?:Genuine )?Intel\(R\)[^\n]*?)[ \t]*$/im);
      if (cpu) q.set("cpu", cpu[1].split(/\s+/).join(" "));
      var seen = {}, pci = [];
      var add = function (v, d) { var x = (v + ":" + d).toLowerCase(); if (!seen[x]) { seen[x] = true; pci.push(x); } };
      var m, re = /\b0x([0-9a-f]{4}):0x([0-9a-f]{4})\b/gi;
      while ((m = re.exec(text))) add(m[1], m[2]);
      re = /\[([0-9a-f]{4}):([0-9a-f]{4})\]/gi;
      while ((m = re.exec(text))) add(m[1], m[2]);
      var vendor = "";
      text.split("\n").forEach(function (line) {
        var v = line.match(/Vendor:.*\(0x([0-9a-f]{4})\)/i), d = line.match(/Device ID:\s*0x([0-9a-f]{4})/i);
        if (v) vendor = v[1]; else if (d && vendor) { add(vendor, d[1]); vendor = ""; }
      });
      if (pci.length) q.set("pci", pci.join(","));
      e.preventDefault();
      window.location = "/identify?" + (q.toString() || "none=1");
    });
  }
})();
