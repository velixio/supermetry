(() => {
  var data = document.currentScript.dataset, api = window.supermetry;
  if (data.heatmap != "true") return;
  let points = [];
  api._clearHeatmap = () => { points = []; };
  const root = document.documentElement;
  function bound(value, max) { return Math.max(0, Math.min(max, Math.round(value))); }
  function click(event) {
    if (!api._allowed()) { points = []; return; }
    const width = root.scrollWidth || 1;
    if (points.length < 200) points.push([
      bound(event.pageX * 1000 / width, 1000), bound(event.pageY, 100000)
    ]);
  }
  var pageURL = location.href;
  function send() {
    if (!api._allowed()) { points = []; return; }
    if (!points.length) return;
    api._post({
      site: data.site, name: "heatmap", url: pageURL, screen_width: screen.width,
      clicks: points, doc_h: bound(root.scrollHeight, 100000), vw: bound(innerWidth, 10000)
    });
    points = [];
  }
  document.addEventListener("click", click);
  addEventListener("pagehide", send);
  api._nav.push(() => { send(); pageURL = location.href; });
})();
