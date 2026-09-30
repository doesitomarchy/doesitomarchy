// doesitomarchy: progressive enhancement only. Every feature below has a
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
    function current(all) { return all.findIndex(function (a) { return a.getAttribute("aria-selected") === "true"; }); }
    function select(all, i) {
      all.forEach(function (a) { a.removeAttribute("aria-selected"); });
      if (i >= 0 && all[i]) { all[i].setAttribute("aria-selected", "true"); all[i].scrollIntoView({ block: "nearest" }); }
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
})();
