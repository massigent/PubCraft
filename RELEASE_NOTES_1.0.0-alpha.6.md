# PubCraft 1.0.0-alpha.6 — Reinforced stability: section-type TOC policy, safe PDF publication

**Status: CANDIDATE, NOT YET PUBLISHED.** This is the preparation document for the next pre-release. There are no alpha.6 downloads yet; the newest published version remains [alpha.5](https://github.com/massigent/PubCraft/releases/tag/v1.0.0-alpha.5).

Alpha.6 focuses on reinforced stability: one consistent section-type policy across the printed TOC and the EPUB nav/NCX, full CLI/GUI/`book.yaml` loading and validation parity for the table-of-contents boolean options, a PDF page-number cache that cannot silently reuse numbers from a different rendering environment, and a publication guard that protects the previously published PDF when a new build fails.

## What's changed (planned)

### Section-type/TOC policy

- Chapter `type:` values in `book.yaml` are section-type strings (including the `colophon` alias of `copyright`), not numeric indices; there are no numeric indices to be out of range. A single policy table (`TOC_POLICY` in `src/pubcraft.c`, mirrored in `src/pubcraftgui/booktypes.go`) now drives the printed TOC, EPUB nav.xhtml and toc.ncx consistently: dedication, epigraph and copyright/colophon are front matter excluded from the printed index but included in the ebook navigation, subheadings are opt-in (`toc.subheads`, default false).
- Unknown types are reported as a warning and paginated as normal chapters; contents are preserved and the value in `book.yaml` is never rewritten.

### CLI / GUI / `book.yaml` parity

- The `toc:` boolean options (`subheads`, `pagenumbers`) are loaded and validated the same way by the CLI, the GUI and `book.yaml`, with the same defaults and the same boolean syntax. Parity tests (`src/pubcraftgui/yaml_parity_test.go`) and YAML fixtures (`tests/fixtures/yaml/07..09`) lock this in.

### PDF index-page cache keyed on the environment

- The printed index measures page numbers on the real PDF and caches them. The cache is now tied to the rendering environment (renderer and page setup), so changing the environment invalidates it instead of reusing stale numbers in the table of contents.

### Publication guard protects the previous PDF

- Publishing a print PDF is now guarded: if the candidate PDF fails validation (for example the page-size check), the previously published PDF stays untouched and the build fails with an explicit message.

### Preview warns on changed sources

- The publication preview shows a warning when the Markdown/`book.yaml` sources changed since the last render, so you are never looking at an outdated preview unknowingly.

### Test hygiene and build tooling

- The browser test suite was consolidated into 19 runners with a shared cleanup helper (`tests/cleanup.cjs`), removing duplicated setup and making failures easier to reproduce.
- The AppImage workflow pins appimagetool to release 1.9.1, so Linux image builds use a fixed tool version.

## Honest status / limits

- Alpha.6 remains an alpha **candidate**: the **local** build/test cycle for the alpha.6 sources has **passed** (`make check` with uncached `-race -count=1` Go tests, `go vet`, C core checks, CLI tests, Python release-guard tests, PDF E2E render tests, the full 19-runner browser suite and `check_release_version.py`), but native Linux/Windows package tests and the native macOS package cycle are still pending, and no alpha.6 tag, package or asset has been published. Do not treat this branch as a release.
- A local alpha.6 GUI build exists for testing, but there are no official alpha.6 packages, tag or published assets; the README downloads still point to alpha.5.
- A few tests are skipped for environmental reasons only (external Scrivener template fixture not set; optional local renderer/helper tests; the macOS-only WebKit geometry E2E is skipped on non-macOS hosts by design).
- Nothing in this release claims general availability ("stable"), distributor compliance, Developer ID signing or Apple notarization. No RPM package is promised.

---

## Italiano

# PubCraft 1.0.0-alpha.6 — Stabilità rafforzata: politica dei tipi di sezione negli indici, pubblicazione PDF sicura

**Stato: CANDIDATA, NON ANCORA PUBBLICATA.** Questo è il documento di preparazione della prossima pre-release. Non esistono ancora download alfa 6; la versione pubblicata più recente resta [alfa 5](https://github.com/massigent/PubCraft/releases/tag/v1.0.0-alpha.5).

L'alfa 6 punta a una stabilità rafforzata: un'unica politica coerente per i tipi di sezione nell'indice di stampa e nella navigazione EPUB (nav/NCX), piena parità di caricamento e validazione dei booleani dell'indice fra CLI, GUI e `book.yaml`, una cache dei numeri di pagina PDF che non può riusare numeri di un ambiente di rendering diverso, e una guardia di pubblicazione che protegge il PDF già pubblicato quando un nuovo build fallisce.

## Cosa cambia (previsto)

- **Politica tipi di sezione/indice:** i valori `type:` in `book.yaml` sono stringhe di tipo di sezione (incluso l'alias `colophon` di `copyright`), non indici numerici: non esistono riferimenti numerici fuori intervallo da correggere. Un'unica tabella di politica (`TOC_POLICY` in `src/pubcraft.c`, rispecchiata in `src/pubcraftgui/booktypes.go`) governa in modo coerente l'indice di stampa, nav.xhtml e toc.ncx dell'EPUB: dedication, epigraph e copyright/colophon sono frontmatter esclusi dall'indice di stampa ma presenti nella navigazione EPUB, i sottocapi sono opt-in (`toc.subheads`, default falso).
- I tipi sconosciuti producono un avviso e sono impaginati come capitoli normali; i contenuti restano e il valore in `book.yaml` non viene mai riscritto.
- **Parità CLI/GUI/YAML:** le opzioni dell'indice accettate da CLI, GUI e `book.yaml` coincidono, con gli stessi valori predefiniti, e sono coperte da test dedicati.
- **Cache delle pagine dell'indice PDF legata all'ambiente:** cambiare renderer o formato invalida la cache invece di riusare numeri di pagina obsoleti nell'indice.
- **Guardia di pubblicazione:** se il PDF candidato fallisce la validazione (per esempio il controllo del formato pagina), il PDF pubblicato in precedenza resta intatto e la build si interrompe con un messaggio esplicito.
- **Avvisi di anteprima:** l'anteprima di pubblicazione segnala quando i sorgenti sono cambiati dall'ultimo render.
- **Igiene dei test e tooling:** suite browser consolidata in 19 runner con helper di pulizia condiviso; appimagetool fissato alla versione 1.9.1 nel workflow Linux.

## Stato onesto / limiti

- L'alfa 6 resta una **candidata**: il ciclo di build/test **locale** per i sorgenti alfa 6 è **riuscito** (`make check` con test Go non memorizzati in cache `-race -count=1`, `go vet`, verifiche core C, suite CLI, test Python delle guardie di release, test E2E di rendering PDF e l'intera suite browser in 19 runner, più `check_release_version.py`), ma i test nativi dei pacchetti Linux/Windows e il ciclo macOS nativo sono ancora in sospeso e non esistono tag, pacchetti o asset alfa 6 pubblicati. Questo ramo non è una release.
- Esiste una build GUI locale dell'alfa 6 per i test, ma non esistono pacchetti ufficiali, tag o asset pubblicati per l'alfa 6; i download del README puntano ancora all'alfa 5.
- Alcuni test sono saltati solo per motivi ambientali (fixture esterna di template Scrivener non impostata; test che richiedono un renderer/helper locale opzionale; il test E2E WebKit della geometria, solo macOS, è saltato su host non macOS per progetto).
- I pacchetti Windows dell'alfa 5 pubblicata hanno superato la CI Windows nativa; solo i pacchetti Windows locali di sviluppo creati in questa sessione, prima del bump di versione, sono stati controllati in cross-build. I test nativi dei pacchetti macOS sono ancora in sospeso per questa versione.
- Nessuna pretesa di disponibilità generale ("stabile"), di conformità ai distributori, di firma Developer ID o di notarizzazione Apple. Non è previsto un pacchetto RPM.
