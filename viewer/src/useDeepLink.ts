import { useEffect, useRef } from "preact/hooks";
import type { Chapter, Stop } from "./manifest";

/**
 * Keeps the URL and the tour in sync, both ways.
 *
 * A stop's URL is `#stop-<id>` and a chapter's is `#chapter-<n>` — the same
 * fragments the Go side uses as element ids in its JS-off rendering, so a
 * shared link lands in the same place whether or not this script runs.
 */
export function useDeepLink({
  activeId,
  stopsById,
  chapters,
  activate,
  goToChapter,
}: {
  activeId: string | null;
  stopsById: Map<string, Stop>;
  chapters: Chapter[];
  activate: (id: string, opts?: { scroll?: boolean }) => void;
  goToChapter: (index: number) => void;
}) {
  // Guards the hashchange our own write triggers.
  const suppress = useRef(false);

  const applyHash = () => {
    const frag = decodeURIComponent((location.hash || "").replace(/^#/, ""));
    if (!frag) return false;

    const chapter = /^chapter-(\d+)$/.exec(frag);
    if (chapter) {
      const i = Number(chapter[1]) - 1;
      if (i >= 0 && i < chapters.length) {
        goToChapter(i);
        return true;
      }
      return false;
    }

    const id = frag.replace(/^stop-/, "");
    if (stopsById.has(id)) {
      activate(id);
      return true;
    }
    return false;
  };

  // Restore from the URL on load, else open on the first stop.
  useEffect(() => {
    if (!applyHash()) {
      const first = stopsById.keys().next();
      if (!first.done) activate(first.value, { scroll: false });
    }
    // Intentionally once: this is initial state, not a subscription.
  }, []);

  useEffect(() => {
    const onHashChange = () => {
      if (suppress.current) return;
      applyHash();
    };
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, [stopsById, chapters]);

  // Write the active stop back to the URL so it can be copied and shared.
  // replaceState, not pushState: stepping through a tour with the arrow keys
  // should not bury the page the reader arrived from under a hundred entries.
  useEffect(() => {
    if (!activeId) return;
    const want = `#stop-${activeId}`;
    if (location.hash === want) return;

    suppress.current = true;
    history.replaceState(null, "", want);
    // Clear after the event this write would have queued.
    const t = setTimeout(() => (suppress.current = false), 0);
    return () => clearTimeout(t);
  }, [activeId]);
}
