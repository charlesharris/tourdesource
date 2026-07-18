// tour-de-source viewer (skeleton).
//
// Reads the inlined manifest + code from #tds-data (no fetch, so it works when
// index.html is opened directly via file://), renders a two-pane view — a
// narrative rail on the left, a code pane on the right — and drives the code
// pane to the active stop's anchor. Scrollytelling and free-browse land in
// later tasks; this establishes the structure and click/keyboard stepping.
(function () {
  "use strict";

  var data = JSON.parse(document.getElementById("tds-data").textContent);
  var manifest = data.manifest || {};
  var code = data.code || {};
  var app = document.getElementById("tds-app");

  app.innerHTML =
    '<header class="tds-header"></header>' +
    '<main class="tds-main">' +
    '<nav class="tds-rail" id="tds-rail"></nav>' +
    '<section class="tds-code" id="tds-code"><div class="tds-code-empty">Select a stop to see its code.</div></section>' +
    "</main>";

  var rail = document.getElementById("tds-rail");
  var codePane = document.getElementById("tds-code");

  // Header.
  var header = app.querySelector(".tds-header");
  var head = "<h1>" + esc(manifest.title || "Tour") + "</h1>";
  if (manifest.audience) head += '<p class="tds-audience">For ' + esc(manifest.audience) + "</p>";
  if (manifest.commit) head += '<p class="tds-commit">@ ' + esc(String(manifest.commit).slice(0, 12)) + "</p>";
  header.innerHTML = head;

  var stopsById = {};
  var order = []; // stop ids in document order, for keyboard stepping

  if (manifest.intro) rail.appendChild(html("div", "tds-intro", manifest.intro));

  var chapters = manifest.chapters || [];
  if (chapters.length) rail.appendChild(renderOutline(chapters));

  chapters.forEach(function (ch, i) {
    var section = document.createElement("section");
    section.className = "tds-chapter";
    section.id = "chapter-" + (i + 1);
    var h2 = document.createElement("h2");
    h2.textContent = ch.title || "";
    section.appendChild(h2);
    if (ch.intro) section.appendChild(html("div", "tds-chapter-intro", ch.intro));
    (ch.stops || []).forEach(function (st) { section.appendChild(renderStop(st)); });
    rail.appendChild(section);
  });

  // The outline is the tour's table of contents. On a whole-project tour the
  // chapters are subsystems, so this is the reader's map of what is covered and
  // their way to jump straight to the part they came for.
  function renderOutline(chs) {
    var nav = document.createElement("nav");
    nav.className = "tds-toc";
    nav.setAttribute("aria-label", "Tour contents");

    var h2 = document.createElement("h2");
    h2.className = "tds-toc-title";
    h2.textContent = "Contents";
    nav.appendChild(h2);

    var ol = document.createElement("ol");
    chs.forEach(function (ch, i) {
      var li = document.createElement("li");
      var a = document.createElement("a");
      a.href = "#chapter-" + (i + 1);
      a.textContent = ch.title || "Chapter " + (i + 1);
      a.addEventListener("click", function (e) {
        e.preventDefault();
        goToChapter(i);
      });
      li.appendChild(a);

      var n = countStops(ch.stops);
      var count = document.createElement("span");
      count.className = "tds-toc-count";
      count.textContent = n === 1 ? "1 stop" : n + " stops";
      li.appendChild(count);

      ol.appendChild(li);
    });
    nav.appendChild(ol);
    return nav;
  }

  // Counts nested detour stops too, so the outline reflects how much there is
  // to read rather than just the top-level count.
  function countStops(stops) {
    var n = 0;
    (stops || []).forEach(function (st) {
      n += 1;
      (st.detours || []).forEach(function (d) { n += countStops(d.stops); });
    });
    return n;
  }

  // Jumping to a chapter activates its first stop, so the code pane follows the
  // narrative instead of going stale against a heading.
  function goToChapter(i) {
    var section = rail.querySelector("#chapter-" + (i + 1));
    if (section) section.scrollIntoView({ block: "start" });
    var first = firstStopOf(chapters[i]);
    if (first) activate(first, { scroll: false });
  }

  function firstStopOf(ch) {
    var stops = (ch && ch.stops) || [];
    return stops.length ? stops[0].id : null;
  }

  function renderStop(st) {
    stopsById[st.id] = st;
    order.push(st.id);

    var wrap = document.createElement("article");
    wrap.className = "tds-stop";
    wrap.setAttribute("data-stop-id", st.id);
    wrap.tabIndex = 0;

    var loc = document.createElement("div");
    loc.className = "tds-stop-loc";
    loc.textContent = locationLabel(st.anchor);
    if (st.anchor && !st.anchor.resolved) loc.classList.add("tds-unresolved");
    wrap.appendChild(loc);

    wrap.appendChild(html("div", "tds-prose", st.prose || ""));

    (st.detours || []).forEach(function (d) {
      var det = document.createElement("details");
      det.className = "tds-detour";
      var sum = document.createElement("summary");
      sum.textContent = d.title || "Detour";
      det.appendChild(sum);
      if (d.intro) det.appendChild(html("div", "tds-prose", d.intro));
      (d.stops || []).forEach(function (ds) { det.appendChild(renderStop(ds)); });
      wrap.appendChild(det);
    });

    wrap.addEventListener("click", function (e) { e.stopPropagation(); activate(st.id); });
    return wrap;
  }

  function activate(id, opts) {
    var st = stopsById[id];
    if (!st) return;
    opts = opts || {};

    var prev = rail.querySelector(".tds-stop.tds-active");
    if (prev) prev.classList.remove("tds-active");
    var node = rail.querySelector('[data-stop-id="' + cssEscape(id) + '"]');
    if (node) {
      node.classList.add("tds-active");
      if (opts.scroll !== false) node.scrollIntoView({ block: "nearest" });
    }

    // Keep the URL pointing at the current stop so it can be copied and shared.
    // replaceState, not pushState: stepping through a tour with the arrow keys
    // should not bury the page the reader arrived from under a hundred entries.
    setHash("stop-" + id);

    var a = st.anchor || {};
    if (!a.resolved || !code[a.path]) {
      codePane.innerHTML = '<div class="tds-code-empty">' +
        esc(a.reason || "No code is available for this stop.") + "</div>";
      return;
    }
    codePane.innerHTML = code[a.path];
    highlightRange(a.start_line, a.end_line);
  }

  function highlightRange(start, end) {
    var lines = codePane.querySelectorAll(".chroma .line");
    if (!lines.length) return;
    for (var i = start - 1; i <= end - 1 && i < lines.length; i++) {
      if (i >= 0) lines[i].classList.add("tds-hl");
    }
    var target = lines[start - 1] || lines[0];
    if (target) target.scrollIntoView({ block: "center" });
  }

  document.addEventListener("keydown", function (e) {
    if (!order.length) return;
    var active = rail.querySelector(".tds-stop.tds-active");
    var idx = active ? order.indexOf(active.getAttribute("data-stop-id")) : -1;
    if (e.key === "ArrowDown" || e.key === "ArrowRight") {
      e.preventDefault();
      activate(order[Math.min(order.length - 1, idx + 1)] || order[0]);
    } else if (e.key === "ArrowUp" || e.key === "ArrowLeft") {
      e.preventDefault();
      activate(order[Math.max(0, idx - 1)] || order[0]);
    }
  });

  function locationLabel(a) {
    if (!a) return "";
    if (a.symbol) return a.symbol;
    if (a.path) {
      var r = a.start_line === a.end_line ? a.start_line : a.start_line + "-" + a.end_line;
      return a.path + ":" + r;
    }
    return a.raw || "";
  }

  function html(tag, cls, innerHTML) {
    var el = document.createElement(tag);
    el.className = cls;
    el.innerHTML = innerHTML;
    return el;
  }
  function esc(s) {
    var d = document.createElement("div");
    d.textContent = s == null ? "" : String(s);
    return d.innerHTML;
  }
  function cssEscape(s) { return String(s).replace(/["\\]/g, "\\$&"); }

  // --- deep links ---------------------------------------------------------
  //
  // A stop's URL is "#stop-<id>" and a chapter's is "#chapter-<n>" — the same
  // fragments the JS-off rendering uses as element ids, so a shared link lands
  // in the same place whether or not the script runs.

  var suppressHashChange = false;

  function setHash(frag) {
    if (("#" + frag) === location.hash) return;
    suppressHashChange = true;
    if (window.history && history.replaceState) {
      history.replaceState(null, "", "#" + frag);
    } else {
      location.hash = frag; // ancient browsers: fall back to a real hash write
    }
    // The flag guards the hashchange the write itself triggers; clear it after
    // that event would have fired rather than leaving it latched on.
    setTimeout(function () { suppressHashChange = false; }, 0);
  }

  // applyHash restores the state a fragment names. Returns false when the
  // fragment names nothing we know, so the caller can fall back.
  function applyHash() {
    var frag = decodeURIComponent(String(location.hash || "").replace(/^#/, ""));
    if (!frag) return false;

    var m = /^chapter-(\d+)$/.exec(frag);
    if (m) {
      var i = parseInt(m[1], 10) - 1;
      if (i >= 0 && i < chapters.length) {
        goToChapter(i);
        return true;
      }
      return false;
    }
    var id = frag.replace(/^stop-/, "");
    if (stopsById[id]) {
      activate(id);
      return true;
    }
    return false;
  }

  window.addEventListener("hashchange", function () {
    if (suppressHashChange) return;
    applyHash();
  });

  // Restore from the URL, else open on the first stop.
  if (!applyHash() && order.length) activate(order[0]);
})();
