import { render } from "preact";
import { App } from "./App";
import { readPayload } from "./manifest";
import "./viewer.css";

// The Go side renders a complete, readable version of the tour into #tds-app
// before this runs (see internal/viewer). Mounting replaces it with the
// interactive two-pane app; if this script never runs, or throws, the reader
// keeps the static narrative rather than an empty page.
const root = document.getElementById("tds-app");
if (root) {
  try {
    const payload = readPayload();
    root.innerHTML = "";
    root.classList.add("tds-app-interactive");
    render(<App payload={payload} />, root);
  } catch (err) {
    // Leave the static rendering in place and say why, rather than failing
    // silently with a half-empty document.
    console.error("tds viewer failed to start; showing the static tour", err);
  }
}
