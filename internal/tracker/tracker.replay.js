(() => {
  var script = document.currentScript, data = script.dataset;
  var api = window.supermetry;
  if (!api || !data.replay) return;
  var granted = api._privacyState ?? (data.replay == "never" || data.replay == "auto" && data.consent != "required");
  function start() {
    var connection = navigator.connection;
    if (!granted || !api._allowed() || api._replay || connection?.saveData || /^(slow-)?2g$/.test(connection?.effectiveType)) return;
    // Replay lives next to the events endpoint: keep a first-party proxy's
    // path prefix (/sm/api/public/v1/events -> /sm). A custom event path has
    // no known suffix, so it falls back to the origin.
    var events = new URL(data.api || "/api/public/v1/events", location.href);
    var prefix = events.pathname.match(/^(.*)\/api\/public\/v1\/events\/?$/);
    var base = events.origin + (prefix ? prefix[1] : "");
    // The site's neutral events path (<base>/e/<site>) carries the recorder
    // and its uploads under the same base, away from "replay" URLs.
    var path = events.pathname.replace(/\/$/, "");
    var neutral = path.endsWith("/e/" + data.site) ? events.origin + path : "";
    api._replay = {s: data.site, o: base, p: neutral};
    var replayScript = document.createElement("script");
    replayScript.src = neutral ? neutral + "/r.js" : base + "/replay.js";
    document.head.appendChild(replayScript);
  }
  api.replay = {trigger: (reason) => api._replayTrigger = reason || "manual"};
  api._privacy = (allowed) => {
    granted = allowed;
    api._replayConsent = allowed;
    api._replayDenied = !allowed;
    if (!allowed) api._replayStop?.();
    else start();
  };
  if (granted) start();
})();
