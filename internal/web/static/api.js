// /api only: one language for every sample, and an index that follows the
// page. Without script, every sample shows, labelled.
(function () {
  "use strict";
  function showLang(lang) {
    document.querySelectorAll(".code[data-tabs]").forEach(function (c) {
      var body = c.querySelector('.code-body[data-lang="' + lang + '"]');
      if (!body) return;
      c.querySelectorAll(".code-body").forEach(function (b) { b.classList.toggle("on", b === body); });
      c.querySelectorAll(".langs button").forEach(function (b) { b.setAttribute("aria-pressed", b.getAttribute("data-lang") === lang ? "true" : "false"); });
      c.querySelector("[data-copy]").setAttribute("data-copy", body.querySelector("code").id);
    });
  }
  document.addEventListener("click", function (e) {
    var b = e.target.closest && e.target.closest(".langs button");
    if (!b) return;
    var lang = b.getAttribute("data-lang");
    // Keep the clicked panel where it is while the others change height.
    var top = b.getBoundingClientRect().top;
    showLang(lang);
    window.scrollBy(0, b.getBoundingClientRect().top - top);
    try { localStorage.setItem("apilang", lang); } catch (err) {}
  });
  var apiNav = document.querySelector(".apidoc-nav");
  if (apiNav) {
    try { var saved = localStorage.getItem("apilang"); if (saved) showLang(saved); } catch (err) {}
    var navLinks = {}, sections = [];
    apiNav.querySelectorAll('a[href^="#"]').forEach(function (a) {
      var sec = document.getElementById(a.getAttribute("href").slice(1));
      if (sec) { navLinks[sec.id] = a; sections.push(sec); }
    });
    var current = null, queued = false;
    var spy = function () {
      queued = false;
      // The current section is the last one whose top has passed a line a
      // third of the way down; at the very bottom, the last section.
      var line = window.innerHeight / 3, cur = sections[0];
      sections.forEach(function (s) { if (s.getBoundingClientRect().top <= line) cur = s; });
      if (window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 2) {
        var last = sections[sections.length - 1];
        if (last.getBoundingClientRect().top < window.innerHeight) cur = last;
      }
      if (cur === current) return;
      if (current) navLinks[current.id].removeAttribute("aria-current");
      var a = navLinks[cur.id];
      a.setAttribute("aria-current", "true");
      current = cur;
      // As a bar of jump links (narrow screens), keep the current one in view.
      if (apiNav.scrollWidth > apiNav.clientWidth) apiNav.scrollTo({ left: a.offsetLeft - 16, behavior: "smooth" });
    };
    window.addEventListener("scroll", function () { if (!queued) { queued = true; requestAnimationFrame(spy); } }, { passive: true });
    window.addEventListener("resize", spy);
    spy();
  }
})();
