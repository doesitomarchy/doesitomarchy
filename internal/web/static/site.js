// DoesItOmarchy: progressive enhancement only. Every feature below has a
// no-JS fallback (plain links and forms), and nothing here uses eval.
(function () {
  "use strict";
  var root = document.documentElement;

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

  function celebrate() {
    active = true;
    var saved = [];
    document.querySelectorAll(".stat").forEach(function (st) {
      var rect = st.querySelector("svg.bar rect"), n = st.querySelector(".figure .n"), p = st.querySelector(".figure .p");
      var total = st.querySelector(".figure").getAttribute("data-total");
      saved.push([rect, rect.getAttribute("width"), n, n.textContent, p, p.textContent]);
      rect.setAttribute("width", "100");
      n.textContent = total;
      p.textContent = "100.0%";
    });
    konami.hidden = false;
    var armed = false, timer;
    setTimeout(function () { armed = true; }, 1000); // ignore the hand still on the keyboard or mouse
    function reset() {
      if (!armed) return;
      clearTimeout(timer);
      saved.forEach(function (s) { s[0].setAttribute("width", s[1]); s[2].textContent = s[3]; s[4].textContent = s[5]; });
      konami.hidden = true;
      ["keydown", "mousemove", "mousedown", "touchstart", "wheel"].forEach(function (ev) { removeEventListener(ev, reset, true); });
      active = false;
    }
    ["keydown", "mousemove", "mousedown", "touchstart", "wheel"].forEach(function (ev) { addEventListener(ev, reset, true); });
    timer = setTimeout(function () { armed = true; reset(); }, 10000);
  }

  function flashThenCelebrate() {
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
})();
