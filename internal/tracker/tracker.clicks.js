(() => {
  var api = window.supermetry, track = api?.track, data = document.currentScript.dataset;
  if (!track || data.clicks == "false") return;
  function tagged(element) {
    var name, props = {}, count = 0;
    for (var value of element.classList || []) {
      if (value.indexOf("supermetry-event-") != 0) continue;
      var pair = value.slice(17).replace(/\+/g, " "), split = pair.indexOf("=");
      if (split < 0) name = name || pair;
      else if (split && split <= 64 && pair.length - split - 1 <= 256 && ++count <= 20) {
        props[pair.slice(0, split)] = pair.slice(split + 1);
      }
    }
    return name && {name: name, props: props};
  }
  api._tagged = tagged;
  function custom(event) {
    var element = event.target;
    for (var level = 0; element && level < 4; level++, element = element.parentElement) {
      var name = element.getAttribute?.("data-supermetry-event");
      if (name) {
        var props = {}, bytes = 0, count = 0;
        for (var attribute of element.attributes) {
          if (attribute.name.indexOf("data-supermetry-prop-") != 0) continue;
          var key = attribute.name.slice(21, 96), value = attribute.value.slice(0, 500);
          if (!key || (bytes += key.length + value.length) > 700 || ++count > 30) break;
          props[key] = value;
        }
        track(name.slice(0, 75), props);
        return;
      }
      var tag = tagged(element);
      if (tag) {
        track(tag.name.slice(0, 75), tag.props);
        return;
      }
    }
  }
  document.addEventListener("click", custom);
  document.addEventListener("submit", custom);
})();
