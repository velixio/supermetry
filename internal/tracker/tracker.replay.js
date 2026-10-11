(() => {
  var script = document.currentScript, data = script.dataset;
  var api = window.supermetry;
  if (!api || !data.replay) return;
  var granted = api._privacyState ?? (data.replay == "never" || data.replay == "auto" && data.consent != "required");
  function start() {
    var connection = navigator.connection;
    if (!granted || !api._allowed() || api._replay || connection?.saveData || /^(slow-)?2g$/.test(connection?.effectiveType)) return;
    var origin = new URL(data.api || "/api/public/v1/events", location.href).origin;
    api._replay = {s: data.site, o: origin};
    var replayScript = document.createElement("script");
    replayScript.src = origin + "/replay.js";
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
