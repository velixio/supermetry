(function () {
  var s = document.currentScript, d = s.dataset;
  var t = window.panel && window.panel.track;
  if (!t) return;

  function link(e) {
    if (e.button > 1) return;
    var a = e.target.closest && e.target.closest("a");
    if (!a) return;
    var u = a;
    var p = {url: (u.origin + u.pathname + u.hash).slice(0, 500)};
    if (d.outbound != "false" && u.protocol.indexOf("http") == 0 && u.host != location.host) {
      t("Outbound Link: Click", p);
    }
    var x = (d.downloadExt || "pdf,xlsx,docx,csv,txt,zip,rar,7z,gz,dmg,exe,msi,pkg,apk,mp3,mp4,mov,avi,wav,key,pptx,ppt,xls,doc,epub,iso").toLowerCase().split(",");
    if (d.downloads != "false" && x.indexOf(u.pathname.toLowerCase().split(".").pop()) >= 0) {
      t("File Download", p);
    }
  }

  function custom(e) {
    if (d.clicks == "false") return;
    var a = e.target;
    for (var l = 0; a && l < 4; l++, a = a.parentElement) {
      var n = a.getAttribute && a.getAttribute("data-panel-event");
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
      try {
        run(observer, observer.takeRecords());
      } catch (_) {}
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
      var props = {}, k;
      if (largest != null) props.lcp = "" + Math.round(largest);
      if (cls) props.cls = "" + Math.round(cls * 1000);
      if (inp) props.inp = "" + Math.round(inp);
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
}());
