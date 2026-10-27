# PubCraft 1.0.0-alpha.4 — Chapter movement and Corkboard reordering

**Seamless chapter repositioning with dedicated move arrows and drag-and-drop across the sidebar and Corkboard (Lavagna a Schede).**

This pre-release re-introduces and refines the chapter reordering workflow. It brings back dedicated up/down arrows in the sidebar and extends chapter movement directly to the Corkboard cards, with smooth drag-and-drop and instant synchronization to `book.yaml`. It includes all Universal 2 macOS distribution and durable release safeguards from [alpha.3](https://github.com/massigent/PubCraft/releases/tag/v1.0.0-alpha.3) and [alpha.2](https://github.com/massigent/PubCraft/releases/tag/v1.0.0-alpha.2).

## Download / Scarica

### macOS — Intel + Apple Silicon, macOS 13 Ventura or newer (Universal 2)

- **[Download PubCraft.app ZIP](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.4/PubCraft-1.0.0-alpha.4-macos-universal.app.zip)** — extract the archive and move `PubCraft.app` to Applications.
- **[DMG](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.4/PubCraft-1.0.0-alpha.4.dmg)** — open and drag the same Universal app to Applications.
- [PKG installer](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.4/PubCraft-1.0.0-alpha.4.pkg)
- [Portable CLI/GUI/helper archive](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.4/PubCraft-1.0.0-alpha.4-macos.tar.gz)

### Other platforms

- **Windows x64:** [Installer](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.4/PubCraft-Setup-1.0.0-alpha.4.exe) · [Portable ZIP](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.4/PubCraft-1.0.0-alpha.4-windows-x64.zip)
- **Linux x64:** [AppImage](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.4/PubCraft-1.0.0-alpha.4-x86_64.AppImage) · [Debian/Ubuntu](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.4/PubCraft-1.0.0-alpha.4-linux-amd64.deb) · [Portable archive](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.4/PubCraft-1.0.0-alpha.4-linux-x64.tar.gz)
- [SHA-256 checksums](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.4/checksums-sha256.txt)

## What's changed

### Re-introduced and enhanced chapter moving

- **Sidebar Chapter List:** Each chapter now features dedicated move arrows (`▲` up and `▼` down). The first chapter has the up arrow disabled; the last chapter has the down arrow disabled. The arrows are subtly visible at all times and illuminate on hover and focus.
- **Corkboard (Lavagna a Schede):** Index cards now include `◀` (previous/earlier) and `▶` (next/later) arrow buttons directly in the footer next to "Apri ↗", making card reordering effortless without having to leave the Corkboard.
- **Smooth Drag-and-Drop:** Both sidebar chapters and Corkboard cards can now be dragged directly to any desired spot. Edge detection (`drag-over-top`/`drag-over-bottom` in the sidebar, `drag-over-left`/`drag-over-right` in Corkboard) provides clear visual feedback of insertion placement.
- **Accidental Click Prevention:** Drag operations are properly decoupled from click handlers so dropping a card will never trigger accidental chapter opening or unexpected view switching.
- **Immediate Server Sync:** Chapter order updates are saved immediately to `book.yaml` via `/api/reorder`, broadcast to the live Watch preview, and update Focus mode selectors.
- **Full Localization:** All 10 supported languages (English, Italian, Spanish, French, German, Portuguese, Chinese, Japanese, Russian, Arabic) include localized titles and accessibility labels for the movement controls.
- **Automated Regression Suite:** Added `tests/test_chapter_reorder.cjs` covering sidebar movement, boundary button states, corkboard arrows, drag-and-drop and disk persistence.

---

## Italiano

**Ripristinato e potenziato lo spostamento dei capitoli con frecce dedicate e trascinamento sia nella barra laterale che nella Lavagna a Schede (Corkboard).**

### Novità principali

- **Elenco capitoli nella barra laterale:** Sono tornate le frecce per spostare i capitoli in su (`▲`) e in giù (`▼`). Il primo capitolo ha la freccia su disabilitata, l'ultimo capitolo ha la freccia giù disabilitata. Le frecce sono visibili in modo sobrio ed elegante e si evidenziano al passaggio del mouse.
- **Lavagna a Schede (Corkboard):** Ogni scheda dispone ora di comodissimi pulsanti freccia `◀` (sposta prima) e `▶` (sposta dopo) nel piè di pagina della scheda, accanto al tasto "Apri ↗", per riordinare la trama con un solo clic.
- **Trascinamento (Drag-and-Drop) fluido:** Puoi anche prendere qualsiasi capitolo o scheda e trascinarlo direttamente nella posizione desiderata. Un indicatore visivo mostra dove verrà posizionato il capitolo (prima o dopo la scheda target).
- **Nessuna apertura accidentale:** Risolto il problema per cui il rilascio del trascinamento apriva involontariamente l'editor: la vista rimane stabile sulla Lavagna.
- **Salvataggio immediato:** L'ordine viene salvato subito in `book.yaml` e sincronizzato con l'anteprima Live Watch e i selettori di capitolo.
