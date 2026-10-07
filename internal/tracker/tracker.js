(function () {
  "use strict";

  var script = document.currentScript;
  var data = script && script.dataset;
  var host = location.hostname;
  if (!data || !data.site || navigator.webdriver ||
      !script.hasAttribute("data-allow-localhost") &&
      (host === "localhost" || host.slice(-10) === ".localhost" ||
       host.slice(0, 4) === "127." || host === "[::1]")) {
    return;
  }

  var endpoint = data.api || "/api/public/v1/events";
  var pageURL = location.href;
  var active = 0;
  var started = document.hidden ? 0 : Date.now();
  var maxScroll = 0;
  var pending = true;

  function send(name, props, engagement, scroll) {
    var body = JSON.stringify({
      site: data.site,
      name: name,
      url: pageURL,
      referrer: document.referrer,
      screen_width: screen.width,
      props: props || {},
      engagement_ms: engagement || 0,
      scroll_pct: scroll || 0
    });
    if (navigator.sendBeacon && navigator.sendBeacon(endpoint,
        new Blob([body], {type: "text/plain"}))) {
      return;
    }
    fetch(endpoint, {
      method: "POST",
      headers: {"Content-Type": "text/plain"},
      body: body,
      keepalive: true
    }).catch(function () {});
  }

  function stopTimer() {
    if (started) {
      active += Date.now() - started;
      started = 0;
    }
  }

  function flushEngagement() {
    stopTimer();
    updateScroll();
    if (pending) {
      send("engagement", {}, Math.round(active), maxScroll);
      active = maxScroll = 0;
      pending = false;
    }
  }

  function updateScroll() {
    var body = document.body;
    var height = Math.max(document.documentElement.scrollHeight, body ? body.scrollHeight : 0);
    if (height) {
      maxScroll = Math.max(maxScroll, Math.min(100,
        Math.round(100 * (scrollY + innerHeight) / height)));
    }
  }

  function startPage() {
    pageURL = location.href;
    active = maxScroll = 0;
    started = document.hidden ? 0 : Date.now();
    pending = true;
    send("pageview");
  }

  var pushState = history.pushState;
  history.pushState = function () {
    var result = pushState.apply(history, arguments);
    flushEngagement();
    startPage();
    return result;
  };
  addEventListener("popstate", function () {
    flushEngagement();
    startPage();
  });
  addEventListener("scroll", function () {
    updateScroll();
    pending = true;
  }, {passive: true});
  document.addEventListener("visibilitychange", function () {
    if (document.hidden) {
      flushEngagement();
    } else {
      started = Date.now();
      pending = true;
    }
  });
  addEventListener("pagehide", flushEngagement);

  var panel = window.panel = window.panel || {};
  panel.track = function (name, props) { send(name, props); };
  send("pageview");
}());
