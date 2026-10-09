(() => {
  var s = document.currentScript, d = s.dataset;
  var p = window.panel, t = p?.track;
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
      if (n) {
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

      var tag = tagged(a);
      if (tag) {
        t(tag.name.slice(0, 75), tag.props);
        return;
      }
    }
  }

  function tagged(el) {
    var name, props = {}, n = 0;
    for (var c of el.classList || []) {
      if (c.indexOf("panel-event-") != 0) continue;
      var kv = c.slice(12).replace(/\+/g, " "), i = kv.indexOf("=");
      if (i < 0) {
        name = name || kv;
      } else if (i && i <= 64 && kv.length - i - 1 <= 256 && ++n <= 20) {
        props[kv.slice(0, i)] = kv.slice(i + 1);
      }
    }
    return name && {name: name, props: props};
  }

  document.addEventListener("click", link);
  document.addEventListener("auxclick", link);
  document.addEventListener("click", custom);
  document.addEventListener("submit", custom);
  if (d.forms != "false") {
    document.addEventListener("submit", (e) => {
      var f = e.target;
      if (!f || f.tagName != "FORM" || f.getAttribute?.("data-panel-event") || tagged(f)) return;
      var a = (f.getAttribute("action") || "").split(/[?#]/)[0].slice(0, 500);
      t("Form: Submit", {
        form_id: (f.getAttribute("id") || "").slice(0, 100),
        form_name: (f.getAttribute("name") || "").slice(0, 100),
        action: a
      });
    }, true);
  }
  if (d[404] != "false" && (s.hasAttribute("data-404-page") || document.querySelector('meta[name="panel:404"]'))) {
    t("404", {path: location.pathname.slice(0, 500)});
  }

  if (d.vitals != "false" && window.PerformanceObserver) {
    var observers, values, first, last, sum, cls, sent;

    function run(observer, entries) {
      for (var i = 0, entry; i < entries.length; i++) {
        entry = entries[i];
        if (observer.kind == 1) {
          values.lcp = "" + Math.round(entry.startTime);
        } else if (observer.kind == 2) {
          if (entry.hadRecentInput) continue;
          if (entry.startTime - last >= 1000 || entry.startTime - first >= 5000) {
            first = entry.startTime;
            sum = 0;
          }
          sum += entry.value;
          last = entry.startTime;
          cls = Math.max(cls, sum);
          values.cls = "" + Math.round(cls * 1000);
        } else if (entry.interactionId) {
          // INP approximation: retain the worst observed interaction latency.
          values.inp = "" + Math.max(values.inp || 0, Math.round(entry.duration));
        }
      }
    }

    function observe(type, kind, buffered) {
      try {
        var observer = new PerformanceObserver((list) => run(observer, list.getEntries()));
        observer.kind = kind;
        // durationThreshold only applies to "event"; other types ignore it.
        observer.observe({type: type, buffered: buffered, durationThreshold: 40});
        observers.push(observer);
        return observer;
      } catch (_) {}
    }

    function stop(kind) {
      observers.forEach((observer, index) => {
        if (!observer || kind && observer.kind != kind) return;
        if (observer.takeRecords) run(observer, observer.takeRecords());
        observer.disconnect();
        observers[index] = 0;
      });
    }

    // soft: SPA navigation. LCP only exists for the document load, and
    // buffered entries would replay the previous page's shifts and inputs.
    function start(soft) {
      if (observers) stop();
      observers = [];
      values = {};
      cls = first = last = sum = 0;
      sent = false;
      if (!soft) observe("largest-contentful-paint", 1, true);
      observe("layout-shift", 2, !soft);
      observe("event", 3, !soft);
    }

    function sendVitals() {
      if (sent) return;
      sent = true;
      stop();
      if (values.lcp || values.cls || values.inp) t("Web Vitals", values);
    }

    document.addEventListener("pointerdown", () => stop(1), true);
    document.addEventListener("keydown", () => stop(1), true);
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

  if (d.heatmap == "true") {
    let points = [];
    p._clearHeatmap = () => { points = []; };
    const root = document.documentElement;

    function bound(value, max) {
      return Math.max(0, Math.min(max, Math.round(value)));
    }

    function heatClick(e) {
      if (!p._allowed()) { points = []; return; }
      const width = root.scrollWidth || 1;
      if (points.length < 200) points.push([
        bound(e.pageX * 1000 / width, 1000),
        bound(e.pageY, 100000)
      ]);
    }

    function sendHeatmap() {
      if (!p._allowed()) { points = []; return; }
      if (!points.length) return;
      const payload = {
        site: d.site,
        name: "heatmap",
        url: location.href,
        screen_width: screen.width,
        clicks: points,
        doc_h: bound(root.scrollHeight, 100000),
        vw: bound(innerWidth, 10000)
      };
      points = [];
      // _post rechecks consent, opt-out, GPC and DNT at upload time.
      p._post(payload);
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
