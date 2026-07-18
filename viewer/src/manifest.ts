// The compiled tour, as the Go side writes it.
//
// This mirrors internal/manifest/manifest.go and is the contract between the
// two halves of tds. Keep the two in sync: the Go side is authoritative, since
// it also renders the JS-off fallback from the same structure.

export interface Anchor {
  raw: string;
  path: string;
  start_line: number;
  end_line: number;
  symbol?: string;
  /** symbol | line-range | unresolved */
  kind: string;
  resolved: boolean;
  /** matched via the #/. loose fallback */
  loose?: boolean;
  /** why it is unresolved */
  reason?: string;
}

export interface Stop {
  /** stable id, used for deep links */
  id: string;
  anchor: Anchor;
  /** raw focus hint, resolved by the viewer */
  focus?: string;
  view?: string;
  /** rendered HTML */
  prose: string;
  detours?: Detour[];
}

export interface Detour {
  title: string;
  /** rendered HTML */
  intro?: string;
  stops: Stop[];
}

export interface Chapter {
  title: string;
  /** rendered HTML */
  intro?: string;
  stops: Stop[];
}

export interface Manifest {
  version: number;
  title: string;
  template?: string;
  audience?: string;
  repo?: string;
  commit?: string;
  meta?: Record<string, string>;
  /** rendered HTML */
  intro?: string;
  chapters: Chapter[];
  warnings?: string[];
}

export interface Payload {
  manifest: Manifest;
  /** file path -> build-time highlighted HTML */
  code: Record<string, string>;
}

/**
 * Reads the tour data inlined in the document.
 *
 * Never fetched: the page is opened from disk via file://, where fetch and
 * module scripts are blocked. Everything the viewer needs is already in the DOM.
 */
export function readPayload(): Payload {
  const el = document.getElementById("tds-data");
  if (!el?.textContent) {
    throw new Error("tds: no tour data found in the document");
  }
  return JSON.parse(el.textContent) as Payload;
}

/** Every stop in document order, including those nested in detours. */
export function flattenStops(chapters: Chapter[]): Stop[] {
  const out: Stop[] = [];
  const walk = (stops: Stop[]) => {
    for (const s of stops) {
      out.push(s);
      for (const d of s.detours ?? []) walk(d.stops);
    }
  };
  for (const ch of chapters) walk(ch.stops);
  return out;
}

/** Counts a chapter's stops including nested detour stops. */
export function countStops(stops: Stop[]): number {
  let n = 0;
  for (const s of stops) {
    n += 1;
    for (const d of s.detours ?? []) n += countStops(d.stops);
  }
  return n;
}

/** How a stop's location is labelled in the rail. */
export function locationLabel(a: Anchor | undefined): string {
  if (!a) return "";
  if (a.symbol) return a.symbol;
  if (a.path) {
    const range =
      a.start_line === a.end_line
        ? `${a.start_line}`
        : `${a.start_line}-${a.end_line}`;
    return `${a.path}:${range}`;
  }
  return a.raw ?? "";
}
