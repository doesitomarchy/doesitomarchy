// /api/register only: suggest a source ID from the tool name as it's typed.
// The applicant can change it; once they do, the name stops overwriting it
// (clearing the ID starts the suggestions again).
(function () {
  "use strict";
  var name = document.querySelector('.register-form input[name="name"]');
  var id = document.querySelector('.register-form input[name="id"]');
  if (!name || !id) return;
  // The server's rule: 2–32 lower-case letters, digits and '-', starting
  // with a letter.
  function suggest(s) {
    return s.normalize("NFKD").replace(/[̀-ͯ]/g, "").toLowerCase()
      .replace(/[^a-z0-9]+/g, "-").replace(/^[^a-z]+/, "").slice(0, 32).replace(/-+$/, "");
  }
  var own = id.value !== "" && id.value !== suggest(name.value);
  name.addEventListener("input", function () { if (!own) id.value = suggest(name.value); });
  id.addEventListener("input", function () { own = id.value !== ""; });
})();
