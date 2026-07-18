import { useEffect, useRef } from "preact/hooks";
import type { Stop } from "./manifest";

/**
 * Above this many lines, an anchor is marked rather than washed. Chosen to sit
 * just above a typical method: methods should feel highlighted, whole files
 * should feel delimited.
 */
const WIDE_RANGE_LINES = 14;

/**
 * The right-hand pane: the anchored file with the stop's range highlighted.
 *
 * Code arrives already highlighted from the build (internal/highlight), so
 * there is no runtime highlighter to ship or wait for — the markup is inserted
 * as-is and only the highlight range is applied here.
 */
export function CodePane({
  stop,
  code,
}: {
  stop: Stop | undefined;
  code: Record<string, string>;
}) {
  const ref = useRef<HTMLElement>(null);
  const anchor = stop?.anchor;
  const html = anchor?.resolved ? code[anchor.path] : undefined;

  useEffect(() => {
    const el = ref.current;
    if (!el || !anchor || !html) return;

    const lines = el.querySelectorAll<HTMLElement>(".chroma .line");
    if (lines.length === 0) return;

    for (const line of lines) line.classList.remove("tds-hl", "tds-hl-wide", "tds-hl-edge");

    // A full-background wash reads well over a method, but a 40-line anchor
    // turns the whole pane into one colour block and stops meaning anything.
    // Past a threshold, mark the span in the gutter and tint it only faintly.
    const span = anchor.end_line - anchor.start_line + 1;
    const wide = span > WIDE_RANGE_LINES;

    for (let i = anchor.start_line - 1; i <= anchor.end_line - 1 && i < lines.length; i++) {
      if (i < 0) continue;
      lines[i]?.classList.add("tds-hl");
      if (wide) lines[i]?.classList.add("tds-hl-wide");
    }
    // Mark the boundaries so a wide span still reads as a bounded region.
    if (wide) {
      lines[anchor.start_line - 1]?.classList.add("tds-hl-edge");
      lines[Math.min(anchor.end_line - 1, lines.length - 1)]?.classList.add("tds-hl-edge");
    }
    // Centre the start of the range rather than the top of the file.
    (lines[anchor.start_line - 1] ?? lines[0])?.scrollIntoView({ block: "center" });
  }, [stop?.id, html, anchor?.start_line, anchor?.end_line]);

  if (!stop) {
    return (
      <section class="tds-code">
        <div class="tds-code-empty">Select a stop to see its code.</div>
      </section>
    );
  }

  if (!html) {
    return (
      <section class="tds-code">
        <div class="tds-code-empty">
          {anchor?.reason || "No code is available for this stop."}
        </div>
      </section>
    );
  }

  return (
    <section class="tds-code">
      <div class="tds-code-head">
        <span class="tds-code-path">{anchor?.path}</span>
        <span class="tds-code-lines">
          {anchor && anchor.start_line === anchor.end_line
            ? `line ${anchor.start_line}`
            : `lines ${anchor?.start_line}–${anchor?.end_line}`}
        </span>
      </div>
      <div
        class="tds-code-body"
        ref={ref as any}
        dangerouslySetInnerHTML={{ __html: html }}
      />
    </section>
  );
}
