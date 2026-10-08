(function () {
  var s = document.currentScript, d = s.dataset;
  var t = window.panel && window.panel.track;
  if (!t) return;
  var extensions = (d.downloadExt || "pdf,xlsx,docx,csv,txt,zip,mp3,mp4,mov,avi,wav,pptx,ppt,xls,doc").toLowerCase().split(",");

  function link(e) {
    if (e.button > 1) return;
    var a = e.target.closest && e.target.closest("a");
    if (!a) return;
    var p = {url: (a.origin + a.pathname + a.hash).slice(0, 500)};
    if (d.outbound != "false" && a.href[0] == "h" && a.host != location.host) {
      t("Outbound Link: Click", p);
    }
    if (d.downloads != "false" && extensions.includes(a.pathname.toLowerCase().split(".").pop())) {
      t("File Download", p);
    }
  }

  function custom(e) {
    if (d.clicks == "false") return;
    var a = e.target.closest && e.target.closest("[data-panel-event]");
    if (!a) return;
    var n = a.getAttribute("data-panel-event"), p = {}, c = 0, b = 0;
    for (var r of a.attributes) {
      if (!r.name.startsWith("data-panel-prop-")) continue;
      var k = r.name.slice(16, 91);
      if (!k) continue;
      var v = r.value.slice(0, 500);
      if (c++ == 30 || (b += k.length + v.length) > 700) break;
      p[k] = v;
    }
    t(n.slice(0, 75), p);
  }

  document.addEventListener("click", function (e) { link(e); custom(e); });
  document.addEventListener("auxclick", link);
  document.addEventListener("submit", custom);
  if (d[404] != "false" && (s.hasAttribute("data-404-page") || document.querySelector('meta[name="panel:404"]'))) {
    t("404", {path: location.pathname.slice(0, 500)});
  }

  if (d.vitals != "false" && window.PerformanceObserver) {
    var lcp, shift, interaction, largest, cls, inp, first, last, sum;

    function run(observer, entries) {
      for (var i = 0, entry; i < entries.length; i++) {
        entry = entries[i];
        if (observer.kind == 1) {
          largest = entry.startTime;
        } else if (observer.kind == 2) {
          if (entry.hadRecentInput) continue;
          if (entry.startTime - last >= 1000 || entry.startTime - first >= 5000) {
            first = entry.startTime;
            sum = 0;
          }
          cls = Math.max(cls, sum += entry.value);
          last = entry.startTime;
        } else if (entry.interactionId) {
          // INP approximation: retain the worst observed interaction latency.
          inp = Math.max(inp, entry.duration);
        }
      }
    }

    function observe(type, kind, buffered) {
      try {
        var observer = new PerformanceObserver(function (list) {
          run(observer, list.getEntries());
        });
        observer.kind = kind;
        // durationThreshold only applies to "event"; other types ignore it.
        observer.observe({type: type, buffered: buffered, durationThreshold: 40});
        return observer;
      } catch (_) {}
    }

    function stop(observer) {
      if (!observer) return;
      if (observer.takeRecords) run(observer, observer.takeRecords());
      observer.disconnect();
    }

    function stopLCP() {
      stop(lcp);
      lcp = 0;
    }

    function stopAll() {
      stopLCP();
      stop(shift);
      stop(interaction);
      shift = interaction = 0;
    }

    // soft: SPA navigation. LCP only exists for the document load, and
    // buffered entries would replay the previous page's shifts and inputs.
    function start(soft) {
      stopAll();
      largest = null;
      cls = inp = first = last = sum = 0;
      if (!soft) lcp = observe("largest-contentful-paint", 1, true);
      shift = observe("layout-shift", 2, !soft);
      interaction = observe("event", 3, !soft);
    }

    function sendVitals() {
      stopAll();
      var props = {}, k;
      if (largest != null) props.lcp = "" + Math.round(largest);
      if (cls) props.cls = "" + Math.round(cls * 1000);
      if (inp) props.inp = "" + Math.round(inp);
      largest = null;
      cls = inp = 0;
      for (k in props) return t("Web Vitals", props);
    }

    document.addEventListener("pointerdown", stopLCP, true);
    document.addEventListener("keydown", stopLCP, true);
    document.addEventListener("visibilitychange", function () {
      if (document.hidden) sendVitals();
    });
    addEventListener("pagehide", sendVitals);
    var navigate = history.pushState;
    history.pushState = function () {
      sendVitals();
      start(1);
      return navigate.apply(history, arguments);
    };
    addEventListener("popstate", function () {
      sendVitals();
      start(1);
    }, true);
    start();
  }

  if (d.heatmap != "false") {
    let points = [];
    const root = document.documentElement;

    function bound(value, max) {
      return Math.max(0, Math.min(max, Math.round(value)));
    }

    function heatClick(e) {
      const width = root.scrollWidth || 1;
      if (points.length < 200) points.push([
        bound(e.clientX * 1000 / width, 1000),
        bound(e.pageY, 100000)
      ]);
    }

    function sendHeatmap() {
      if (!points.length) return;
      const stringify = JSON.stringify;
      JSON.stringify = function (payload) {
        payload.clicks = points;
        payload.doc_h = bound(root.scrollHeight, 100000);
        payload.vw = bound(innerWidth, 10000);
        return stringify(payload);
      };
      t("heatmap");
      JSON.stringify = stringify;
      points = [];
    }

    document.addEventListener("click", heatClick);
    addEventListener("pagehide", sendHeatmap);
    const heatNavigate = history.pushState;
    history.pushState = function () {
      sendHeatmap();
      heatNavigate.apply(history, arguments);
    };
    addEventListener("popstate", sendHeatmap, true);
  }
}());
