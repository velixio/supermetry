(() => {
  var data = document.currentScript.dataset, track = window.supermetry?.track;
  if (!track || data.downloads == "false") return;
  var extensions = (data.downloadExt || "pdf,xlsx,docx,csv,txt,zip,rar,7z,gz,dmg,exe,msi,pkg,apk,mp3,mp4,mov,avi,wav,key,pptx,ppt,xls,doc,epub,iso").toLowerCase().split(",");
  function click(event) {
    if (event.button > 1) return;
    var link = event.target.closest?.("a");
    if (link && extensions.indexOf(link.pathname.toLowerCase().split(".").pop()) >= 0) {
      track("File Download", {url: (link.origin + link.pathname + link.hash).slice(0, 500)});
    }
  }
  document.addEventListener("click", click);
  document.addEventListener("auxclick", click);
})();
