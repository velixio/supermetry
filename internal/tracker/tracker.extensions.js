(() => {
  var s = document.currentScript, d = s.dataset;
  var t = window.panel?.track;
  if (!t) return;

  function link(e) {
    if (e.button > 1) return;
    var a = e.target.closest?.("a");
    if (!a) return;
    var p = {url: (a.origin + a.pathname + a.hash).slice(0, 500)};
    if (d.outbound != "false" && a.protocol.indexOf("http") == 0 && a.host != location.host) {
      t("Outbound Link: Click", p);
    }
    var x = (d.downloadExt || "pdf,xlsx,docx,csv,txt,zip,rar,7z,gz,dmg,exe,msi,pkg,apk,mp3,mp4,mov,avi,wav,key,pptx,ppt,xls,doc,epub,iso").toLowerCase().split(",");
    if (d.downloads != "false" && x.indexOf(a.pathname.toLowerCase().split(".").pop()) >= 0) {
      t("File Download", p);
    }
  }

  function custom(e) {
    if (d.clicks == "false") return;
    var a = e.target;
    for (var l = 0; a && l < 4; l++, a = a.parentElement) {
      var n = a.getAttribute?.("data-panel-event");
      if (!n) continue;
      var p = {};
      for (var i = 0, c = 0, b = 0; i < a.attributes.length && c < 30; i++) {
        var r = a.attributes[i];
        if (r.name.indexOf("data-panel-prop-") != 0) continue;
        var k = r.name.slice(16, 91);
        if (!k) continue;
        var v = r.value.slice(0, 500);
        if ((b += k.length + v.length) > 700) break;
        p[k] = v;
        c++;
      }
      t(n.slice(0, 75), p);
      return;
    }
  }

  document.addEventListener("click", link);
  document.addEventListener("auxclick", link);
  document.addEventListener("click", custom);
  document.addEventListener("submit", custom);
  if (d[404] != "false" && (s.hasAttribute("data-404-page") || document.querySelector('meta[name="panel:404"]'))) {
    t("404", {path: location.pathname.slice(0, 500)});
  }

  if (d.vitals != "false" && window.PerformanceObserver) {
    var lcp, shift, interaction, largest, cls, inp, first, last, sum, sent;

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
          sum += entry.value;
          last = entry.startTime;
          cls = Math.max(cls, sum);
        } else if (entry.interactionId) {
          // INP approximation: retain the worst observed interaction latency.
          inp = Math.max(inp, entry.duration);
        }
      }
    }

    function observe(type, kind, buffered) {
      try {
        var observer = new PerformanceObserver((list) => run(observer, list.getEntries()));
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
      sent = false;
      if (!soft) lcp = observe("largest-contentful-paint", 1, true);
      shift = observe("layout-shift", 2, !soft);
      interaction = observe("event", 3, !soft);
    }

    function sendVitals() {
      if (sent) return;
      sent = true;
      stopAll();
      var props = {};
      if (largest != null) props.lcp = "" + Math.round(largest);
      if (cls) props.cls = "" + Math.round(cls * 1000);
      if (inp) props.inp = "" + Math.round(inp);
      if (largest != null || cls || inp) t("Web Vitals", props);
    }

    document.addEventListener("pointerdown", stopLCP, true);
    document.addEventListener("keydown", stopLCP, true);
    document.addEventListener("visibilitychange", () => document.hidden && sendVitals());
    addEventListener("pagehide", sendVitals);

    var navigate = history.pushState;
    history.pushState = (...args) => {
      navigate.apply(history, args);
      sendVitals();
      start(1);
    };
    addEventListener("popstate", () => sendVitals() || start(1));
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
        bound(e.pageX * 1000 / width, 1000),
        bound(e.pageY, 100000)
      ]);
    }

    function sendHeatmap() {
      if (!points.length) return;
      const body = JSON.stringify({
        site: d.site,
        name: "heatmap",
        url: location.href,
        screen_width: screen.width,
        clicks: points,
        doc_h: bound(root.scrollHeight, 100000),
        vw: bound(innerWidth, 10000)
      });
      const endpoint = d.api || "/api/public/v1/events";
      points = [];
      if (!(navigator.sendBeacon && navigator.sendBeacon(endpoint, new Blob([body], {type: "text/plain"})))) {
        fetch(endpoint, {method: "POST", headers: {"Content-Type": "text/plain"}, body: body, keepalive: true}).catch(function () {});
      }
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
})();
