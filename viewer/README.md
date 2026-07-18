# The tds viewer

The frontend of a compiled tour: a two-pane reader (narrative rail + code pane)
built with Preact and Vite.

## How it fits together

```
viewer/src/*.tsx  --(npm run build)-->  internal/viewer/assets/viewer.{js,css}
                                                    |
                                                    | go:embed
                                                    v
                                        internal/viewer/viewer.go
                                                    |
                                                    v
                                     a single self-contained index.html
```

**The compiled assets are committed.** `go build` must never require Node — a
contributor building `tds` gets the viewer as it was last compiled. Node is only
needed to *change* the viewer:

```sh
make viewer        # rebuild the assets (run this before committing viewer changes)
make viewer-check  # typecheck
make viewer-dev    # vite dev server
```

If you edit `viewer/src` and forget `make viewer`, your change simply won't be in
the binary. The Go tests assert the compiled bundle is present and non-trivial,
but they cannot tell you it is *stale*.

## Constraints that shape the code

A tour is opened from disk via `file://`, with no server and no network. That
rules out more than it sounds like:

- **No `fetch`, no XHR, no dynamic `import()`.** The tour data is inlined into
  the document as `<script type="application/json" id="tds-data">` and read from
  the DOM. `readPayload()` in `manifest.ts` is the only entry point for it.
- **IIFE output, not ESM.** Module scripts are blocked by CORS on `file://`.
- **No external assets.** No web fonts, no CDN, no image URLs — everything is
  inlined or it isn't there. A Go test fails the build if a load sneaks in.
- **Code is highlighted at build time** (`internal/highlight`), so the viewer
  ships no syntax highlighter and has nothing to wait for.

## Progressive enhancement

`internal/viewer` renders the *entire* tour as static HTML into `#tds-app`
before this script runs. Mounting replaces it. If the script never runs, or
throws, the reader keeps a readable narrative instead of a blank page — so the
static rendering is not a stub, and both sides must agree on the fragment
scheme (`#stop-<id>`, `#chapter-<n>`) for shared links to work either way.

## Theming

`viewer.css` is one layer of custom properties on `:root`, with light and dark
both first class — a tour gets opened by someone whose preference you don't
know. A re-theme should be a change to the token block and nothing else.
