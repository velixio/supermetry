(() => {
  var script = document.currentScript;
  if (script.dataset[404] != "false" && (script.hasAttribute("data-404-page") || document.querySelector('meta[name="supermetry:404"]'))) {
    window.supermetry?.track("404", {path: location.pathname.slice(0, 500)});
  }
})();
