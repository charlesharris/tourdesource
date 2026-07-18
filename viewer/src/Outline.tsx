import type { Chapter } from "./manifest";
import { countStops } from "./manifest";

/**
 * The tour's table of contents.
 *
 * On a whole-project tour the chapters are the subsystems, so this is the
 * reader's map of what is covered and their way to jump straight to the part
 * they came for. Without it a long tour is one undifferentiated scroll.
 */
export function Outline({
  chapters,
  onSelect,
}: {
  chapters: Chapter[];
  onSelect: (index: number) => void;
}) {
  if (chapters.length === 0) return null;

  return (
    <nav class="tds-toc" aria-label="Tour contents">
      <h2 class="tds-toc-title">Contents</h2>
      <ol>
        {chapters.map((ch, i) => {
          const n = countStops(ch.stops);
          return (
            <li key={i}>
              <a
                href={`#chapter-${i + 1}`}
                onClick={(e) => {
                  e.preventDefault();
                  onSelect(i);
                }}
              >
                {ch.title || `Chapter ${i + 1}`}
              </a>
              <span class="tds-toc-count">{n === 1 ? "1 stop" : `${n} stops`}</span>
            </li>
          );
        })}
      </ol>
    </nav>
  );
}
