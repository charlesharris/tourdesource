import { useCallback, useEffect, useMemo, useRef, useState } from "preact/hooks";
import type { Payload, Stop } from "./manifest";
import { flattenStops, locationLabel } from "./manifest";
import { Outline } from "./Outline";
import { CodePane } from "./CodePane";
import { useDeepLink } from "./useDeepLink";

export function App({ payload }: { payload: Payload }) {
  const { manifest, code } = payload;

  // Document order drives keyboard stepping and "next/previous".
  const stops = useMemo(() => flattenStops(manifest.chapters), [manifest]);
  const stopsById = useMemo(
    () => new Map(stops.map((s) => [s.id, s])),
    [stops],
  );

  const [activeId, setActiveId] = useState<string | null>(null);
  const railRef = useRef<HTMLElement>(null);

  const activate = useCallback(
    (id: string, opts: { scroll?: boolean } = {}) => {
      if (!stopsById.has(id)) return;
      setActiveId(id);
      if (opts.scroll === false) return;
      // Bring the stop into view without yanking the page around when it is
      // already visible.
      railRef.current
        ?.querySelector(`[data-stop-id="${CSS.escape(id)}"]`)
        ?.scrollIntoView({ block: "nearest" });
    },
    [stopsById],
  );

  const goToChapter = useCallback(
    (index: number) => {
      railRef.current
        ?.querySelector(`#chapter-${index + 1}`)
        ?.scrollIntoView({ block: "start" });
      // Activating the chapter's first stop keeps the code pane following the
      // narrative instead of going stale against a heading.
      const first = manifest.chapters[index]?.stops[0];
      if (first) activate(first.id, { scroll: false });
    },
    [manifest, activate],
  );

  useDeepLink({ activeId, stopsById, chapters: manifest.chapters, activate, goToChapter });

  // Arrow keys step through the tour. First-class so the presenter/demo use
  // case works without a mouse (design §8).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      const target = e.target as HTMLElement | null;
      if (target && /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName)) return;

      const idx = activeId ? stops.findIndex((s) => s.id === activeId) : -1;
      if (e.key === "ArrowDown" || e.key === "ArrowRight") {
        e.preventDefault();
        activate(stops[Math.min(stops.length - 1, idx + 1)]?.id ?? stops[0]?.id);
      } else if (e.key === "ArrowUp" || e.key === "ArrowLeft") {
        e.preventDefault();
        activate(stops[Math.max(0, idx - 1)]?.id ?? stops[0]?.id);
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [stops, activeId, activate]);

  const active = activeId ? stopsById.get(activeId) : undefined;

  return (
    <>
      <header class="tds-header">
        <h1>{manifest.title || "Tour"}</h1>
        <div class="tds-meta">
          {manifest.audience && <span>For {manifest.audience}</span>}
          {manifest.commit && (
            <span class="tds-commit" title={manifest.commit}>
              @ {manifest.commit.slice(0, 12)}
            </span>
          )}
        </div>
      </header>

      <main class="tds-main">
        <nav class="tds-rail" ref={railRef} aria-label="Tour narrative">
          {manifest.intro && (
            <div
              class="tds-intro"
              dangerouslySetInnerHTML={{ __html: manifest.intro }}
            />
          )}

          <Outline chapters={manifest.chapters} onSelect={goToChapter} />

          {manifest.chapters.map((ch, i) => (
            <section class="tds-chapter" id={`chapter-${i + 1}`} key={i}>
              <h2>{ch.title}</h2>
              {ch.intro && (
                <div
                  class="tds-chapter-intro"
                  dangerouslySetInnerHTML={{ __html: ch.intro }}
                />
              )}
              {ch.stops.map((s) => (
                <StopBlock
                  key={s.id}
                  stop={s}
                  activeId={activeId}
                  onActivate={activate}
                />
              ))}
            </section>
          ))}
        </nav>

        <CodePane stop={active} code={code} />
      </main>
    </>
  );
}

function StopBlock({
  stop,
  activeId,
  onActivate,
}: {
  stop: Stop;
  activeId: string | null;
  onActivate: (id: string) => void;
}) {
  const isActive = stop.id === activeId;
  const unresolved = !stop.anchor?.resolved;

  return (
    <article
      class={`tds-stop${isActive ? " tds-active" : ""}`}
      id={`stop-${stop.id}`}
      data-stop-id={stop.id}
      tabIndex={0}
      onClick={(e) => {
        e.stopPropagation();
        onActivate(stop.id);
      }}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onActivate(stop.id);
        }
      }}
    >
      <div class={`tds-stop-loc${unresolved ? " tds-unresolved" : ""}`}>
        {locationLabel(stop.anchor)}
      </div>
      <div class="tds-prose" dangerouslySetInnerHTML={{ __html: stop.prose }} />

      {(stop.detours ?? []).map((d, i) => (
        <details class="tds-detour" key={i}>
          <summary>{d.title || "Detour"}</summary>
          {d.intro && (
            <div class="tds-prose" dangerouslySetInnerHTML={{ __html: d.intro }} />
          )}
          {d.stops.map((ds) => (
            <StopBlock
              key={ds.id}
              stop={ds}
              activeId={activeId}
              onActivate={onActivate}
            />
          ))}
        </details>
      ))}
    </article>
  );
}
