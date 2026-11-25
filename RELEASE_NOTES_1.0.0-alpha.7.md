# PubCraft 1.0.0-alpha.7 — robustness update: faithful import/export round-trip, safe print convergence, Windows instance fixes

**Status: pre-release (alpha).** This is the 1.0.0-alpha.7 source; it is alpha software, not a stable or GA release. Pre-release downloads for this version are published as release assets on the [alpha.7 release page](https://github.com/massigent/PubCraft/releases/tag/v1.0.0-alpha.7) in the next step of the cycle. The previous published version remains available as a fallback: [alpha.6](https://github.com/massigent/PubCraft/releases/tag/v1.0.0-alpha.6).

Alpha.7 is a robustness update born from a full review of the code: fourteen real defects were fixed across the CLI and the GUI. There are no new end-user features. The value is in content round-trip fidelity (exporting a manuscript and re-importing it no longer turns prose into structures), in a bounded and verifiable convergence of print pagination, in GUI instance reliability on Windows, and in EPUB metadata consistency with the specification.

## What's changed

### CLI (`src/pubcraft.c`)

- **Live `dcterms:modified`:** the EPUB OPF metadata timestamp is now the real UTC build time instead of a hard-coded date. EPUB 3 requires the date and time of the last modification; previously every book of every build claimed the same fixed value.
- **Bounded KDP gutter convergence:** the printed-PDF path (`build-print`/`export-print` with `--pdf`) recalculates margins when the real page count crosses a KDP gutter band. The recalculation is now bounded by an explicit budget; a book oscillating on a band boundary no longer rebuilds forever and stops with a clear warning suggesting `--pages`.
- **Faithful `export-md` round-trip:** lines that merely *start* with Markdown structure (headings, images, block quotes, lists, numbered items, tables, fenced code) are escaped by the writer and forced to plain text by the loader instead of being re-parsed as structure. This is backwards compatible with the old `\#` and `\!` escapes.
- **Front matter delimiters:** a closing front-matter line of three or more dashes no longer corrupts the chapter body (the leftover dash used to turn the first body line into a list item).
- **Chapter-name validation:** `is_safe_chapter_file` no longer rejects legitimate file names that contain `..` inside a single name (e.g. `Part..1.md`). Separators and leading dots are still rejected, so no path component can ever be `..`; the unchanged security chain is documented inline and unit-tested.
- **`cmd_list` on Windows:** the archive listing uses PowerShell/.NET (`System.IO.Compression.ZipFile`) like the rest of the Windows code path, instead of shelling out to a Unix `unzip` that does not exist there.
- **Boolean `bleed:` option:** `book.yaml` `bleed:` uses the shared tolerant boolean grammar (`true/false`, `yes/no`, `on/off`, `1/0`), the same as `toc.subheads`/`toc.pagenumbers`, and an invalid value fails the build with an explicit error instead of silently defaulting to `false`.
- **`--option=value` print flags:** `--bleed`, `--no-bleed`, `--pdf` and `--no-toc-pagenumbers` accept the `--flag=value` form, consistently with `--trim`/`--pages`/`--toc-pagemap`.
- **Hardened binary-plist parser:** the array/dictionary node allocations are checked before writing; a malformed third-party archive degrades to a null node instead of risking a crash.

### GUI (`src/pubcraftgui`)

- **Process existence on Windows:** `processExists` relied on `Process.Signal(syscall.Signal(0))`, which always fails on Windows. This broke the single-instance launcher (a live instance was always considered dead) and made the preview-directory janitor delete the preview folders of *other running instances*. Windows now probes with `OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)` via the standard library, treating access-denied as "exists" (build-tagged; the Unix implementation is unchanged).
- **Project alignment for chapter reordering:** `apiReorder` now honours the `X-PubCraft-Vault` header already sent by the web client and rejects with 409 a reorder request belonging to another open project, like `apiSave` and `apiBuild` already did. Clients without the header keep working.
- **CSRF hardening:** the security middleware now also requires POST + JSON on five mutating endpoints (`/api/publication-preview` and the recents/workspace mutation endpoints) and guards the only GET endpoint with side effects (`/api/watch/pdf?rebuild=1`) against cross-site embedding via `Sec-Fetch-Site` (documented limitation: browsers without `Sec-Fetch-Site` are allowed).
- **Recent-projects hygiene:** the recents filter no longer excludes paths via hard-coded test-specific patterns, which could also exclude real user vaults; it now skips only vaults inside the system temporary directory plus the explicit `PUBCRAFT_TESTING` gate.
- **Build timeout:** CLI invocations get a 5-minute budget (configurable via `runToolTimeout`) instead of a fixed 120 s, so large illustrated books no longer fail mid-build.

### Tests

- New internal C suites in `tests/check_core.c`: `roundtrip-guards` (escape predicate, chapter names, front-matter delimiters) and `loader-unescape` (end-to-end loader round-trip on a real temporary vault).
- `endpoint_hardening_test.go`: project alignment of chapter reordering and the `Sec-Fetch-Site` guard, with their expected outcomes.
- `process_unix_test.go`: process existence (live pid, terminated child).

## Validation performed

All changes passed, on a Linux x64 host at preparation time:

- `gcc -Wall -Wextra -fsyntax-only` on the C sources.
- The C internal checks (`tests/check_core.c`: 3 suites — TOC policy, round-trip guards, loader unescape), 0 failures.
- The CLI shell suite (`tests/test_cli.sh`, 15 tests).
- `go vet` on Linux and Windows.
- The uncached Go suite (`go test -race -count=1`); the PDF end-to-end tests were skipped because Chromium is absent on that host.
- Cross-compilation for linux/amd64, windows/amd64, darwin/amd64 and darwin/arm64 (compile checks only).
- The Python release-guard tests (`python3 -m unittest discover -s tests -p test_release_guards.py`) and `check_release_version.py`.
- A manual end-to-end round-trip smoke on a real vault (escaped lines render as plain paragraphs in the EPUB).

The native package builds (Linux/Windows/macOS Universal, with the two native Intel/ARM jobs) are executed by the `release-build.yml` workflow on the `release/1.0.0-alpha.7` branch and recorded in the validation record.

Details: [release validation record](docs/RELEASE_VALIDATION_1.0.0-alpha.7.md). SHA-256 checksums for all release assets accompany the release.

## Honest status / limits

- This is alpha software: **not** a general-availability ("stable") release, and not a distributor certification. Keep backups and work on copies; review every import/export.
- The macOS app is ad-hoc signed; it is **not** Developer ID signed and not notarized by Apple. The PKG and the Windows installer are not publisher-signed.
- The tag, the draft release and the asset upload/publication are the next step of the cycle and are not done yet.
- Declared limits: the browser suite and the PDF end-to-end tests were not executed on the preparation host (environment); native Windows/macOS execution is only available through CI; the macOS launcher `openFiles:` ordering and `cmd.exe` quoting for exotic paths still need native verification (no known regression).

---

## Italiano

# PubCraft 1.0.0-alpha.7 — aggiornamento di robustezza: round-trip import/export fedele, convergenza di stampa sicura, fix dell'istanza Windows

**Stato: pre-release (alfa).** Questo è il sorgente 1.0.0-alpha.7; è software alfa, non una release stabile o GA. I download pre-release di questa versione vengono pubblicati come asset della [pagina release alfa 7](https://github.com/massigent/PubCraft/releases/tag/v1.0.0-alpha.7) nel passo successivo del ciclo. La versione pubblicata precedente resta disponibile come ripiego: [alfa 6](https://github.com/massigent/PubCraft/releases/tag/v1.0.0-alpha.6).

L'alfa 7 è un aggiornamento di robustezza nato da una revisione completa del codice: sono stati corretti quattordici difetti reali fra CLI e GUI. Non ci sono nuove funzionalità per l'utente finale. Il valore sta nella fedeltà del round-trip dei contenuti (esportare un manoscritto e reimportarlo non trasforma più la prosa in strutture), in una convergenza limitata e verificabile dell'impaginazione di stampa, nell'affidabilità dell'istanza GUI su Windows e nella coerenza della metadata EPUB con la specifica.

## Cosa cambia

### CLI (`src/pubcraft.c`)

- **`dcterms:modified` reale:** il timestamp nella metadata OPF dell'EPUB è ora l'ora UTC effettiva della build invece di una data fissa nel codice. EPUB 3 richiede data e ora dell'ultima modifica; prima ogni libro di ogni build dichiarava lo stesso valore fisso.
- **Convergenza del gutter KDP limitata:** il percorso del PDF di stampa (`build-print`/`export-print` con `--pdf`) ricalcola i margini quando il numero di pagine reale attraversa una fascia del gutter KDP. Il ricalcolo è ora limitato da un budget esplicito; un libro che oscilla su un confine di fascia non ricompila più all'infinito e si ferma con un avviso chiaro che suggerisce `--pages`.
- **Round-trip `export-md` fedele:** le righe che semplicemente *iniziano* con una struttura Markdown (titoli, immagini, citazioni, liste, voci numerate, tabelle, blocchi di codice delimitati) vengono scritte con escape e rilette come testo puro dal loader, invece di essere reinterpretate come struttura. È compatibile con i vecchi escape `\#` e `\!`.
- **Delimitatori del front matter:** una riga di chiusura del front matter composta da tre o più trattini non corrompe più il corpo del capitolo (il trattino residuo trasformava la prima riga del corpo in una voce di lista).
- **Validazione dei nomi capitolo:** `is_safe_chapter_file` non rifiuta più nomi di file legittimi che contengono `..` all'interno di un singolo nome (per esempio `Part..1.md`). Separatori e punti iniziali restano rifiutati, quindi nessun componente di percorso può mai essere `..`; la catena di sicurezza, invariata, è documentata nel codice e coperta da test unitari.
- **`cmd_list` su Windows:** l'elenco dell'archivio usa PowerShell/.NET (`System.IO.Compression.ZipFile`) come il resto del percorso Windows, invece di invocare un `unzip` Unix che lì non esiste.
- **Opzione booleana `bleed:`:** in `book.yaml` `bleed:` usa la grammatica booleana tollerante condivisa (`true/false`, `yes/no`, `on/off`, `1/0`), la stessa di `toc.subheads`/`toc.pagenumbers`, e un valore non valido fa fallire la build con un errore esplicito invece di ripiegare silenziosamente su `false`.
- **Flag di stampa `--opzione=valore`:** `--bleed`, `--no-bleed`, `--pdf` e `--no-toc-pagenumbers` accettano la forma `--flag=valore`, in coerenza con `--trim`/`--pages`/`--toc-pagemap`.
- **Parser bplist rafforzato:** le allocazioni dei nodi array/dizionario sono verificate prima della scrittura; un archivio di terze parti malformato degrada a un nodo nullo invece di rischiare un crash.

### GUI (`src/pubcraftgui`)

- **Esistenza del processo su Windows:** `processExists` si basava su `Process.Signal(syscall.Signal(0))`, che su Windows fallisce sempre. Fra l'altro questo rompeva il launcher a istanza singola (un'istanza viva era sempre considerata morta) e faceva sì che il servizio di pulizia delle anteprime cancellasse le cartelle di anteprima di *altre istanze in esecuzione*. Windows ora sonda con `OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)` tramite la libreria standard, considerando "accesso negato" come "esiste" (file con build tag; l'implementazione Unix è invariata).
- **Allineamento del progetto nel riordino dei capitoli:** `apiReorder` ora rispetta l'header `X-PubCraft-Vault` già inviato dal client web e rifiuta con 409 una richiesta di riordino appartenente a un altro progetto aperto, come già facevano `apiSave` e `apiBuild`. I client che non inviano l'header continuano a funzionare.
- **Rafforzamento CSRF:** il middleware di sicurezza richiede ora POST + JSON anche su cinque endpoint mutanti (`/api/publication-preview` e gli endpoint di mutazione di recent/workspace) e protegge l'unico endpoint GET con effetti (`/api/watch/pdf?rebuild=1`) dall'inclusione cross-site tramite `Sec-Fetch-Site` (limite documentato: i browser senza `Sec-Fetch-Site` sono ammessi).
- **Igiene dei recent:** il filtro dei recent non esclude più percorsi tramite schemi di test fissi nel codice, che potevano escludere anche vault reali dell'utente; ora salta solo i vault dentro la directory temporanea di sistema più il gate esplicito `PUBCRAFT_TESTING`.
- **Timeout delle build:** le invocazioni della CLI hanno ora un budget di 5 minuti (configurabile con `runToolTimeout`) invece dei 120 s fissi, così i grandi libri illustrati non falliscono più a metà build.

### Test

- Nuove suite interne C in `tests/check_core.c`: `roundtrip-guards` (predicato di escape, nomi dei capitoli, delimitatori del front matter) e `loader-unescape` (round-trip end-to-end del loader su un vault reale temporaneo).
- `endpoint_hardening_test.go`: allineamento del progetto nel riordino e guardia `Sec-Fetch-Site`, con gli esiti attesi.
- `process_unix_test.go`: esistenza del processo (pid vivo, figlio terminato).

## Validazione eseguita

Tutte le modifiche sono passate, su un host Linux x64 al momento della preparazione:

- `gcc -Wall -Wextra -fsyntax-only` sui sorgenti C.
- Le verifiche interne C (`tests/check_core.c`: 3 suite — politica dell'indice, guardie del round-trip, unescape del loader), 0 fallimenti.
- La suite shell della CLI (`tests/test_cli.sh`, 15 test).
- `go vet` su Linux e Windows.
- La suite Go non memorizzata in cache (`go test -race -count=1`); i test end-to-end dei PDF sono stati saltati perché su quell'host Chromium è assente.
- Cross-compilazione per linux/amd64, windows/amd64, darwin/amd64 e darwin/arm64 (solo compile-check).
- I test Python delle guardie di release (`python3 -m unittest discover -s tests -p test_release_guards.py`) e `check_release_version.py`.
- Uno smoke end-to-end manuale del round-trip su un vault reale (le righe con escape vengono rese come paragrafi puri nell'EPUB).

Le build native dei pacchetti (Linux/Windows/macOS Universal, con i due job nativi Intel/ARM) sono eseguite dalla workflow `release-build.yml` sul ramo `release/1.0.0-alpha.7` e registrate nel record di validazione.

Dettagli: [record di validazione](docs/RELEASE_VALIDATION_1.0.0-alpha.7.md). I checksum SHA-256 di tutti gli asset accompagnano la release.

## Stato onesto / limiti

- Software alfa: **non** è una release di disponibilità generale ("stabile") e non è una certificazione di distributore. Conserva backup e lavora su copie; controlla ogni importazione/esportazione.
- L'app macOS ha firma ad-hoc: **non** è firmata Developer ID e non è notarizzata Apple. PKG e installer Windows non sono firmati da un editore verificato.
- Il tag, la draft release e l'upload/pubblicazione degli asset sono il passo successivo del ciclo e non sono ancora stati eseguiti.
- Limiti dichiarati: la suite browser e i test end-to-end dei PDF non sono stati eseguiti sull'host di preparazione (ambiente); l'esecuzione nativa Windows/macOS è disponibile solo via CI; l'ordine `openFiles:` del launcher macOS e il quoting `cmd.exe` con percorsi esotici restano da verificare nativamente (nessuna regressione nota).
