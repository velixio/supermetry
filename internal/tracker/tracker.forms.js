(() => {
  var api = window.supermetry;
  if (document.currentScript.dataset.forms == "false") return;
  document.addEventListener("submit", (event) => {
    var form = event.target;
    if (!form || form.tagName != "FORM" || form.getAttribute?.("data-supermetry-event") || api._tagged?.(form)) return;
    api.track("Form: Submit", {
      form_id: (form.getAttribute("id") || "").slice(0, 100),
      form_name: (form.getAttribute("name") || "").slice(0, 100),
      action: (form.getAttribute("action") || "").split(/[?#]/)[0].slice(0, 500)
    });
  }, true);
})();
