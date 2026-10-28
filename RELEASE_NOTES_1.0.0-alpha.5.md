# PubCraft 1.0.0-alpha.5 — Reliable board dragging and folder synchronization

**Corkboard cards can finally be dragged anywhere on the board, and PubCraft now keeps `book.yaml` in sync with the Markdown files you edit outside the app.**

This pre-release fixes the chapter-dragging regression on the Corkboard (Lavagna a Schede) and adds non-destructive synchronization between the book folder and `book.yaml`. It includes all Universal 2 macOS distribution and durable release safeguards from [alpha.4](https://github.com/massigent/PubCraft/releases/tag/v1.0.0-alpha.4), [alpha.3](https://github.com/massigent/PubCraft/releases/tag/v1.0.0-alpha.3) and [alpha.2](https://github.com/massigent/PubCraft/releases/tag/v1.0.0-alpha.2).

## Download / Scarica

### macOS — Intel + Apple Silicon, macOS 13 Ventura or newer (Universal 2)

- **[Download PubCraft.app ZIP](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.5/PubCraft-1.0.0-alpha.5-macos-universal.app.zip)** — extract the archive and move `PubCraft.app` to Applications.
- **[DMG](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.5/PubCraft-1.0.0-alpha.5.dmg)** — open and drag the same Universal app to Applications.
- [PKG installer](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.5/PubCraft-1.0.0-alpha.5.pkg)
- [Portable CLI/GUI/helper archive](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.5/PubCraft-1.0.0-alpha.5-macos.tar.gz)

### Other platforms

- **Windows x64:** [Installer](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.5/PubCraft-Setup-1.0.0-alpha.5.exe) · [Portable ZIP](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.5/PubCraft-1.0.0-alpha.5-windows-x64.zip)
- **Linux x64:** [AppImage](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.5/PubCraft-1.0.0-alpha.5-x86_64.AppImage) · [Debian/Ubuntu](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.5/PubCraft-1.0.0-alpha.5-linux-amd64.deb) · [Portable archive](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.5/PubCraft-1.0.0-alpha.5-linux-x64.tar.gz)
- [SHA-256 checksums](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.5/checksums-sha256.txt)

## What's changed

### Corkboard (Lavagna a Schede): drag a card anywhere

- **The bug:** the board relied on HTML5 drag-and-drop with drop handlers attached only to the cards. Releasing a card on empty board space, in the gap between two cards or below the last row silently did nothing and the card snapped back to its slot. That is fixed.
- **New behaviour:** dragging uses Pointer Events. The card follows the cursor with a floating preview, a vertical accent line shows exactly where it will land, and the drop is accepted on cards, gaps, empty space and below the last row.
- The board **auto-scrolls** near the top and bottom edges, `Esc` cancels the drag, and a 5 px threshold keeps a normal click opening the chapter (and the synopsis modal) as before.
- Dragging works correctly with **status filters and search active**: the saved order is anchored to the target chapter file, not to a visible index.
- The `◀`/`▶` buttons remain available as an accessible alternative.
- **Honest regression suite:** `tests/test_chapter_reorder.cjs` no longer calls `window.reorderChaptersByDrop()` directly; it performs real mouse drags and fails against the previous implementation.

### Folder ↔ `book.yaml` synchronization (non-destructive)

Writing Markdown outside PubCraft no longer leaves the book out of date:

- **New `.md` files** in the book folder are appended to `book.yaml` automatically — title from the front-matter or the first heading, natural order — both when the book is opened and while the app is running. The chapter list refreshes within about a second, with no page reload.
- **Renamed files** are re-linked **in place** using the front-matter `id`, so the chapter keeps its position, title and ID and no duplicate chapter is created.
- **Missing files** (deleted or moved away) are never dropped automatically: the entry stays in the list, flagged `⚠ file mancante` in the sidebar and on the Corkboard, and is removed from `book.yaml` only when you ask for it (no file is deleted by that action).
- **The chapter you have open** is reloaded from disk when it changes outside PubCraft, but only if you have no unsaved local edits — your typing is never overwritten.
- Reserved (`book.md`), hidden, nested (`.cestina/`, subfolders) and `README.md` files are ignored, and an invalid `book.yaml` is still never regenerated.
- All `book.yaml` writes are serialized, so automatic synchronization cannot race with reordering or deletion.

### Tests

- New Go tests (`src/pubcraftgui/vault_sync_test.go`): append, rename by `id`, missing-file marking, reserved/hidden/nested exclusion, idempotency, orphan-entry removal, refusal when the file still exists, and no resurrection of trashed chapters.
- New browser suite `tests/test_vault_sync.cjs` (7 scenarios) covering the same flows end-to-end, plus the reload of the open chapter.

---

## Italiano

**Le schede della Lavagna si trascinano finalmente ovunque, e PubCraft ora tiene `book.yaml` allineato ai file Markdown che modifichi fuori dall'app.**

Questa pre-release corregge il difetto del trascinamento nella Lavagna a Schede e introduce una sincronizzazione non distruttiva tra la cartella del libro e `book.yaml`. Include tutte le garanzie di distribuzione Universal 2 e di release di [alfa 4](https://github.com/massigent/PubCraft/releases/tag/v1.0.0-alpha.4), [alfa 3](https://github.com/massigent/PubCraft/releases/tag/v1.0.0-alpha.3) e [alfa 2](https://github.com/massigent/PubCraft/releases/tag/v1.0.0-alpha.2).

### Lavagna a Schede: trascina la scheda dove vuoi

- **Il difetto:** la lavagna usava il drag-and-drop HTML5 con gli handler di rilascio agganciati solo alle schede. Rilasciando una scheda nello spazio vuoto, nel margine tra due schede o sotto l'ultima riga non succedeva nulla e la scheda tornava al suo posto. Ora è corretto.
- **Nuovo comportamento:** il trascinamento usa i Pointer Events. La scheda segue il puntatore con un'anteprima fluttuante, una linea verticale mostra esattamente dove finirà, e il rilascio è valido su schede, spazi tra le schede, area vuota e sotto l'ultima riga.
- La lavagna fa **auto-scroll** avvicinandosi ai bordi, `Esc` annulla il trascinamento e una soglia di 5 px mantiene il clic normale che apre il capitolo (e il modale della sinossi).
- Il trascinamento funziona correttamente anche con **filtri di stato e ricerca attivi**: l'ordine salvato è ancorato al file della scheda di destinazione, non a un indice visibile.
- I pulsanti `◀`/`▶` restano disponibili come alternativa accessibile.
- **Test onesti:** `tests/test_chapter_reorder.cjs` non chiama più direttamente `window.reorderChaptersByDrop()`; esegue trascinamenti reali con il mouse e fallisce sulla versione precedente.

### Sincronizzazione cartella ↔ `book.yaml` (non distruttiva)

Scrivere Markdown fuori da PubCraft non lascia più il libro disallineato:

- I **nuovi file `.md`** nella cartella del libro vengono accodati automaticamente a `book.yaml` — titolo dal front-matter o dal primo heading, ordine naturale — sia all'apertura del libro sia mentre l'app è aperta. La lista dei capitoli si aggiorna in circa un secondo, senza ricaricare la pagina.
- I **file rinominati** vengono ricollegati **sulla stessa posizione** usando l'`id` del front-matter: il capitolo mantiene posizione, titolo e ID e non viene creato nessun doppione.
- I **file mancanti** (cancellati o spostati) non vengono mai rimossi in automatico: la voce resta in elenco con il contrassegno `⚠ file mancante` nella barra laterale e sulla scheda, e viene tolta da `book.yaml` solo su tua richiesta (quell'azione non cancella alcun file).
- Il **capitolo aperto** viene ricaricato dal disco se cambia fuori da PubCraft, ma solo se non hai modifiche locali non salvate: il tuo testo non viene mai sovrascritto.
- I file riservati (`book.md`), nascosti, annidati (`.cestina/`, sottocartelle) e `README.md` vengono ignorati, e un `book.yaml` non valido continua a non essere mai rigenerato.
- Tutte le scritture di `book.yaml` sono serializzate: la sincronizzazione automatica non può più entrare in conflitto con riordino o eliminazione.

### Test

- Nuovi test Go (`src/pubcraftgui/vault_sync_test.go`): accodamento, rinomina via `id`, segnalazione dei file mancanti, esclusione di file riservati/nascosti/annidati, idempotenza, rimozione della voce orfana, rifiuto quando il file esiste ancora e nessuna "resurrezione" dei capitoli cestinati.
- Nuova suite browser `tests/test_vault_sync.cjs` (7 scenari) che copre gli stessi flussi end-to-end, più il ricaricamento del capitolo aperto.

## Installazione macOS (Universal 2, macOS 13+)

L'app è firmata ad-hoc: al primo avvio macOS può mostrare un avviso di sicurezza. Apri `PubCraft.app` con clic destro → *Apri*, oppure esegui `xattr -dr com.apple.quarantine /Applications/PubCraft.app`. Non è firmata/notarizzata con Developer ID e non è certificata da alcun distributore.
