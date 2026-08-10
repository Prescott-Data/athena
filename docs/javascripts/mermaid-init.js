/* Nexus-style Mermaid rendering — self-managed instead of Material's
   built-in integration (class nx-mermaid keeps Material's handler away). */
(function () {
  var mermaidReady = null;

  function loadMermaid() {
    if (!mermaidReady) {
      mermaidReady = import("https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.esm.min.mjs")
        .then(function (m) {
          var mermaid = m.default;
          var dark = document.body.getAttribute("data-md-color-scheme") === "slate";
          mermaid.initialize({
            startOnLoad: false,
            theme: dark ? "dark" : "neutral",
            securityLevel: "strict",
            fontFamily: "Inter, sans-serif"
          });
          return mermaid;
        });
    }
    return mermaidReady;
  }

  var counter = 0;

  function renderAll() {
    var blocks = document.querySelectorAll("pre.nx-mermaid");
    if (!blocks.length) return;
    loadMermaid().then(function (mermaid) {
      blocks.forEach(function (pre) {
        var src = (pre.querySelector("code") || pre).textContent;
        var host = document.createElement("div");
        host.className = "nx-mermaid-diagram";
        pre.replaceWith(host);
        mermaid.render("nx-mermaid-" + (counter++), src)
          .then(function (r) { host.innerHTML = r.svg; })
          .catch(function (e) {
            /* Show the source rather than a blank hole on parse errors */
            host.innerHTML = "";
            var fallback = document.createElement("pre");
            fallback.textContent = src;
            host.appendChild(fallback);
            if (window.console) console.error("Mermaid render failed:", e);
          });
      });
    });
  }

  document.addEventListener("DOMContentLoaded", renderAll);
  if (typeof document$ !== "undefined") {
    document$.subscribe(renderAll);
  }
})();
