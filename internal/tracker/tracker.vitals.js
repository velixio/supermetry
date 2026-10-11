(() => {
  var track = window.supermetry?.track;
  if (!track || document.currentScript.dataset.vitals == "false" || !window.PerformanceObserver) return;
  var observers, values, first, last, sum, cls, sent;
  function run(observer, entries) {
    for (var entry of entries) {
      if (observer.kind == 1) values.lcp = "" + Math.round(entry.startTime);
      else if (observer.kind == 2) {
        if (entry.hadRecentInput) continue;
        if (entry.startTime - last >= 1000 || entry.startTime - first >= 5000) first = entry.startTime, sum = 0;
        sum += entry.value;
        last = entry.startTime;
        cls = Math.max(cls, sum);
        values.cls = "" + Math.round(cls * 1000);
      } else if (entry.interactionId) values.inp = "" + Math.max(values.inp || 0, Math.round(entry.duration));
    }
  }
  function observe(type, kind, buffered) {
    try {
      var observer = new PerformanceObserver((list) => run(observer, list.getEntries()));
      observer.kind = kind;
      observer.observe({type: type, buffered: buffered, durationThreshold: 40});
      observers.push(observer);
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
  function send() {
    if (sent) return;
    sent = true;
    stop();
    if (values.lcp || values.cls || values.inp) track("Web Vitals", values);
  }
  document.addEventListener("pointerdown", () => stop(1), true);
  document.addEventListener("keydown", () => stop(1), true);
  document.addEventListener("visibilitychange", () => document.hidden && send());
  addEventListener("pagehide", send);
  window.supermetry._nav.push(() => { send(); start(1); });
  start();
})();
