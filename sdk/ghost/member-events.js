(function () {
  // Ghost redirects back with ?action=signup&success=true after a member
  // confirms a signup and with ?stripe=success after a paid checkout.
  var params = new URLSearchParams(location.search);
  var action = params.get("action") || "";
  var events = [];
  if (params.get("success") === "true" && /^(signup|signup-paid|subscribe)$/.test(action)) events.push("Signup");
  if (params.get("stripe") === "success") events.push("Subscription");
  if (!events.length) return;
  var tries = 0;
  (function send() {
    var supermetry = window.supermetry;
    if (supermetry && supermetry.track) {
      for (var i = 0; i < events.length; i++) supermetry.track(events[i], { source: "ghost_portal" });
    } else if (++tries < 100) {
      setTimeout(send, 100);
    }
  })();
})();
