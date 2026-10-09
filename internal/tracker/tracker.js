(() => {
  var script = document.currentScript;
  var data = script && script.dataset;
  var host = location.hostname;
  if (!data || !data.site || navigator.webdriver ||
      !script.hasAttribute("data-allow-localhost") &&
      /(^|\.)localhost$|^127\.|^\[::1\]$/.test(host)) {
    return;
  }

  /*@full*/
  var manual = data.manual == "true";
  var viewStarted = !manual;
  var lastLocation = location.href;
  /*@end*/
  var endpoint = data.api || "/api/public/v1/events";
  var ignored;
  try { ignored = localStorage.supermetry_ignore; } catch (_) {}
  var pageURL = location.href;
  var active = 0;
  var started = /*@full*/ manual ? 0 : /*@end*/ document.hidden ? 0 : Date.now();
  var maxScroll = 0;
  var pending = /*@full*/ !manual && /*@end*/ true;
  var profileID;

  function permitted() {
    return !ignored && !navigator.globalPrivacyControl && navigator.doNotTrack != "1" && window.doNotTrack != "1" /*@full*/ && !denied && !paused /*@end*/;
  }
  function post(payload) {
    if (!permitted()) return;
    payload = JSON.stringify(payload);
    navigator.sendBeacon?.(endpoint, payload) || fetch(endpoint, {
      method: "POST",
      body: payload,
        keepalive: true
      }).catch(() => 0);
  }

  /*@full*/
  function key(url) {
    return data.hash == "true" ? url : url.split("#")[0];
  }
  function run(payload) {
    var hook = supermetry.beforeSend || window[data.beforeSend];
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
  var gated = data.consent == "required";
  var paused = gated ? [] : 0;
  var denied;
  function emit(payload) {
    if (denied) return;
    if (paused) {
      if (paused.length == 20) {
        var drop = paused.findIndex((event) => event.name != "pageview");
        paused.splice(drop < 0 ? 0 : drop, 1);
      }
      paused.push(payload);
      return;
    }
    payload = run(payload);
    if (payload) post(payload);
  }
  /*@end*/

  function send(name, props, engagement, scroll, revenue, traits) {
    var payload = {
      v: 1,
      ts: Date.now(),
      id: (crypto.randomUUID?.() || Math.random().toString(16)).replace(/\W/g, ""),
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
    emit(payload);
    return;
    /*@end*/
    post(payload);
  }

  function flushEngagement() {
    /*@full*/
    if (!viewStarted) return;
    /*@end*/
    if (started) {
      active += Date.now() - started;
      started = 0;
    }
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
    /*@full*/
    viewStarted = true;
    lastLocation = location.href;
    /*@end*/
    pageURL = url || location.href;
    active = maxScroll = 0;
    started = document.hidden ? 0 : Date.now();
    pending = true;
    send("pageview", props);
  }

  function navigate() {
    /*@full*/
    if (manual || key(location.href) == key(lastLocation)) return;
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
    /*@full*/ if (!viewStarted) return; /*@end*/
    updateScroll();
    pending = true;
  }, {passive: true});
  document.addEventListener("visibilitychange", () =>
    /*@full*/ !viewStarted ? 0 : /*@end*/ document.hidden ?
    flushEngagement() : (started = Date.now(), pending = true));
  addEventListener("pagehide", flushEngagement);

  var supermetry = window.supermetry = window.supermetry || {};
  /*@full*/
  var startReplay;
  var replayGranted;
  if (data.replay) {
    startReplay = () => {
      var connection = navigator.connection;
      if (!permitted() || supermetry._replayDenied || supermetry._replay || connection?.saveData || /^(slow-)?2g$/.test(connection?.effectiveType)) return;
      var origin = new URL(endpoint, location.href).origin;
      supermetry._replay = {s: data.site, o: origin};
      var replayScript = document.createElement("script");
      replayScript.src = origin + "/replay.js";
      document.head.appendChild(replayScript);
    };
    supermetry.replay = {trigger: (reason) => supermetry._replayTrigger = reason || "manual"};
    replayGranted = data.replay == "never" || data.replay == "auto" && !gated;
    if (replayGranted) startReplay();
  }
  supermetry._post = post;
  supermetry._allowed = permitted;
  /*@end*/
  // Opt-out is the only thing the tracker ever stores: a flag, no identifier.
  var opt = (value) => () => {
    ignored = value;
    /*@full*/
    if (value) { profileID = 0; if (paused) paused = []; supermetry._clearHeatmap?.(); }
    /*@end*/
    try { value ? localStorage.supermetry_ignore = 1 : delete localStorage.supermetry_ignore; } catch (_) {}
    /*@full*/
    if (data.replay) {
      supermetry._replayDenied = value;
      if (value) supermetry._replayStop?.();
      else if (replayGranted) startReplay();
    }
    /*@end*/
  };
  supermetry.optOut = opt(1);
  supermetry.optIn = opt(0);
  /*@full*/
  supermetry.pageview = (options) => {
    var url = options?.url;
    try {
      url = url ? new URL(url, location.href).href : location.href;
    } catch (error) {
      return;
    }
    flushEngagement();
    startPage(url, options?.props);
  };
  supermetry.consent = (granted) => {
    supermetry._replayConsent = !!granted;
    denied = !granted;
    if (!granted) { profileID = 0; supermetry._clearHeatmap?.(); }
    if (data.replay) {
      replayGranted = granted;
      supermetry._replayDenied = !granted;
      if (!granted) supermetry._replayStop?.();
    }
    if (!gated) {
      if (granted && startReplay) startReplay();
      return;
    }
    var queued = paused;
    denied = !granted;
    paused = granted ? 0 : [];
    if (granted && queued) queued.forEach(emit);
    if (granted && startReplay) startReplay();
  };
  /*@end*/
  supermetry.track = (name, props, options) => send(name, props, 0, 0, options?.revenue);
  // Identification is memory-only, so call identify on every page load.
  supermetry.identify = (id, traits = {}) => {
    if (data.profiles == "false" || !permitted()) return;
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
