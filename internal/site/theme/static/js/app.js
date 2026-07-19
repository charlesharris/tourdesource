/* app.js — command palette (⌘K), tour keyboard nav, symbol filter.
   No dependencies. Loaded with `defer` from baseof.html. */
(function () {
  "use strict";
  var body = document.body;
  var idxUrl = body.getAttribute("data-index");
  var palette = document.querySelector("[data-palette]");
  var input = palette && palette.querySelector("[data-palette-input]");
  var results = palette && palette.querySelector("[data-palette-results]");
  var INDEX = null, items = [], active = 0;

  function openPalette() {
    if (!palette) return;
    palette.hidden = false;
    input.value = "";
    render("");
    input.focus();
  }
  function closePalette() { if (palette) palette.hidden = true; }

  function ensureIndex() {
    if (INDEX) return Promise.resolve(INDEX);
    if (!idxUrl) { INDEX = []; return Promise.resolve(INDEX); }
    return fetch(idxUrl).then(function (r) { return r.json(); })
      .then(function (d) { INDEX = d; return d; })
      .catch(function () { INDEX = []; return INDEX; });
  }

  function render(q) {
    q = (q || "").trim().toLowerCase();
    var data = INDEX || [];
    var matched = data.filter(function (d) {
      return !q || d.title.toLowerCase().indexOf(q) > -1 || (d.meta || "").toLowerCase().indexOf(q) > -1;
    });
    results.innerHTML = "";
    items = [];
    if (!matched.length) {
      results.innerHTML = '<div class="palette-empty">No matches for \u201c' + q + '\u201d</div>';
      return;
    }
    var order = ["File", "Symbol", "Tour"], groups = {};
    matched.forEach(function (d) { (groups[d.type] = groups[d.type] || []).push(d); });
    order.forEach(function (type) {
      var arr = groups[type];
      if (!arr) return;
      var lbl = document.createElement("div");
      lbl.className = "palette-group-label";
      lbl.textContent = type + "s";
      results.appendChild(lbl);
      arr.slice(0, 6).forEach(function (d) {
        var a = document.createElement("a");
        a.className = "palette-item";
        a.href = d.url;
        var t = document.createElement("span"); t.className = "p-title"; t.textContent = d.title;
        var m = document.createElement("span"); m.className = "p-meta"; m.textContent = d.meta || "";
        a.appendChild(t); a.appendChild(m);
        results.appendChild(a);
        items.push(a);
      });
    });
    active = 0;
    highlight();
  }
  function highlight() {
    items.forEach(function (el, i) { el.classList.toggle("is-active", i === active); });
  }

  document.addEventListener("keydown", function (e) {
    var k = (e.key || "").toLowerCase();
    if ((e.metaKey || e.ctrlKey) && k === "k") {
      e.preventDefault();
      ensureIndex().then(openPalette);
      return;
    }
    if (!palette || palette.hidden) {
      var tag = (e.target && e.target.tagName || "").toLowerCase();
      if ((k === "j" || k === "k") && tag !== "input" && tag !== "textarea") tourNav(k === "j" ? 1 : -1);
      return;
    }
    if (k === "escape") closePalette();
    else if (k === "arrowdown") { e.preventDefault(); active = Math.min(items.length - 1, active + 1); highlight(); }
    else if (k === "arrowup") { e.preventDefault(); active = Math.max(0, active - 1); highlight(); }
    else if (k === "enter") { if (items[active]) location.href = items[active].getAttribute("href"); }
  });

  document.addEventListener("click", function (e) {
    var t = e.target.closest("[data-act]");
    if (!t) return;
    if (t.dataset.act === "palette-open") ensureIndex().then(openPalette);
    else if (t.dataset.act === "palette-close") closePalette();
  });
  if (input) input.addEventListener("input", function () { render(input.value); });

  /* Tour: J/K between stops + active outline entry */
  var stops = [].slice.call(document.querySelectorAll("[data-tour-stop]"));
  var scroller = document.getElementById("scroller");
  function tourNav(dir) {
    if (!stops.length || !scroller) return;
    var top = scroller.scrollTop, idx = 0;
    stops.forEach(function (s, i) { if (s.offsetTop - 20 <= top) idx = i; });
    idx = Math.max(0, Math.min(stops.length - 1, idx + dir));
    scroller.scrollTo({ top: stops[idx].offsetTop - 8, behavior: "smooth" });
  }
  if (stops.length && scroller && "IntersectionObserver" in window) {
    var links = [].slice.call(document.querySelectorAll("[data-stop-link]"));
    var byId = {};
    links.forEach(function (l) { byId[l.getAttribute("href").slice(1)] = l; });
    var obs = new IntersectionObserver(function (es) {
      es.forEach(function (en) {
        if (en.isIntersecting) {
          links.forEach(function (l) { l.classList.remove("is-active"); });
          var l = byId[en.target.id];
          if (l) l.classList.add("is-active");
        }
      });
    }, { root: scroller, rootMargin: "-20% 0px -70% 0px" });
    stops.forEach(function (s) { obs.observe(s); });
  }

  /* A detour stop is deep-linkable, but its <details> starts closed, so a link
     to it would otherwise scroll to nothing. Open every ancestor first, then
     scroll — the browser's own hash handling has already run by this point. */
  function revealHash() {
    var id = location.hash.slice(1);
    if (!id) return;
    var el = document.getElementById(id);
    if (!el) return;
    var open = false;
    for (var n = el.parentNode; n && n !== document; n = n.parentNode) {
      if (n.tagName === "DETAILS" && !n.open) { n.open = true; open = true; }
    }
    if (open) el.scrollIntoView();
  }
  if (document.querySelector("[data-detour]")) {
    revealHash();
    window.addEventListener("hashchange", revealHash);
  }

  /* Symbols: live text filter */
  var sf = document.querySelector("[data-sym-filter]");
  if (sf) {
    sf.addEventListener("input", function () {
      var q = sf.value.trim().toLowerCase();
      [].slice.call(document.querySelectorAll("[data-sym-row]")).forEach(function (r) {
        r.style.display = (!q || r.textContent.toLowerCase().indexOf(q) > -1) ? "" : "none";
      });
    });
  }
})();
