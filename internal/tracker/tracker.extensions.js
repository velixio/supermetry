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
}());
