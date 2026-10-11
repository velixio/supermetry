(() => {
  if (document.currentScript.dataset.outbound == "false") return;
  var track = window.supermetry?.track;
  if (!track) return;
  function click(event) {
    if (event.button > 1) return;
    var link = event.target.closest?.("a");
    if (link && link.protocol.indexOf("http") == 0 && link.host != location.host) {
      track("Outbound Link: Click", {url: (link.origin + link.pathname + link.hash).slice(0, 500)});
    }
  }
  document.addEventListener("click", click);
  document.addEventListener("auxclick", click);
})();
