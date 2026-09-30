// Applies the saved theme before first paint (loaded blocking in <head>).
(function () {
  try {
    var t = localStorage.getItem("theme");
    if (t && t !== "system") document.documentElement.setAttribute("data-theme", t);
  } catch (e) {}
})();
