(() => {
  "use strict";

  var script = document.currentScript;
  var data = script && script.dataset;
  var host = location.hostname;
  if (!data || !data.site || navigator.webdriver ||
      !script.hasAttribute("data-allow-localhost") &&
      /(^|\.)localhost$|^127\.|^\[::1\]$/.test(host)) {
    return;
  }

  var endpoint = data.api || "/api/public/v1/events";
  var pageURL = location.href;
  var active = 0;
  var started = document.hidden ? 0 : Date.now();
  var maxScroll = 0;
  var pending = true;
  var profileID;

  function post(payload) {
    var body = JSON.stringify(payload);
    navigator.sendBeacon?.(endpoint,
      new Blob([body], {type: "text/plain"})) || fetch(endpoint, {
        method: "POST",
        body: body,
        keepalive: true
      }).catch(() => 0);
  }

  /*@full*/
  var manual = data.manual == "true";
  function key(url) {
    return data.hash == "true" ? url : url.split("#")[0];
  }
  function run(payload) {
    var hook = panel.beforeSend || window[data.beforeSend];
    var result;
    if (typeof hook != "function") return payload;
    try {
      result = hook({
        name: payload.name,
        url: payload.url,
        referrer: payload.referrer,
        props: payload.props,
        engagement_ms: payload.engagement_ms,
        scroll_pct: payload.scroll_pct
      });
    } catch (error) {
      return;
    }
    if (!result || typeof result != "object") return;
    ["name", "url", "referrer", "props", "engagement_ms", "scroll_pct"].forEach((field) => {
      if (field in result) payload[field] = result[field];
    });
    return payload;
  }
  function emit(payload) {
    payload = run(payload);
    if (payload) post(payload);
    return true;
  }
  /*@end*/

  function send(name, props, engagement, scroll, revenue, traits) {
    var payload = {
      site: data.site,
      name: name,
      url: pageURL,
      referrer: document.referrer,
      screen_width: screen.width,
      props: props || {},
      engagement_ms: engagement || 0,
      scroll_pct: scroll || 0,
      traits: traits
    };
    if (revenue) {
      payload.revenue = revenue;
    }
    if (profileID) {
      payload.profile_id = profileID;
    }
    /*@full*/
    if (emit(payload)) return;
    /*@end*/
    post(payload);
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
    var height = Math.max(document.documentElement.scrollHeight, body?.scrollHeight || 0);
    if (height) {
      maxScroll = Math.max(maxScroll, Math.min(100,
        Math.round(100 * (scrollY + innerHeight) / height)));
    }
  }

  function startPage(url, props) {
    pageURL = url || location.href;
    active = maxScroll = 0;
    started = document.hidden ? 0 : Date.now();
    pending = true;
    send("pageview", props);
  }

  function navigate() {
    /*@full*/
    if (manual || key(location.href) == key(pageURL)) return;
    /*@end*/
    flushEngagement();
    startPage();
  }

  var pushState = history.pushState;
  history.pushState = (...args) => {
    pushState.apply(history, args);
    navigate();
  };
  addEventListener("popstate", navigate);
  /*@full*/
  if (data.hash == "true") addEventListener("hashchange", navigate);
  var replaceState = history.replaceState;
  history.replaceState = (...args) => {
    replaceState.apply(history, args);
    navigate();
  };
  /*@end*/
  addEventListener("scroll", () => {
    updateScroll();
    pending = true;
  }, {passive: true});
  document.addEventListener("visibilitychange", () => document.hidden ?
    flushEngagement() : (started = Date.now(), pending = true));
  addEventListener("pagehide", flushEngagement);

  var panel = window.panel = window.panel || {};
  /*@full*/
  panel.pageview = (options) => {
    flushEngagement();
    startPage(options?.url, options?.props);
  };
  /*@end*/
  panel.track = (name, props, options) => send(name, props, 0, 0, options?.revenue);
  // Identification is memory-only, so call identify on every page load.
  panel.identify = (id, traits = {}) => {
    if (id === null) {
      return profileID = 0;
    }
    if (id !== String(id) || !id || id.length > 128 ||
        !traits || typeof traits !== "object" || Array.isArray(traits)) {
      return;
    }
    var count = 0;
    for (var key in traits) {
      if (++count > 20 || !key.length || key.length > 64 ||
          typeof traits[key] !== "string" || traits[key].length > 256) {
        return;
      }
    }
    profileID = id;
    send("identify", 0, 0, 0, 0, traits);
  };
  /*@full*/ if (!manual) /*@end*/ send("pageview");
})();
