# PubCraft

**Scrivi il tuo libro in Markdown. Conserva i file sul tuo computer. Esporta EPUB e impaginati di stampa dallo stesso progetto.**

PubCraft è gratuito e open source: editor locale nel browser, compilatore CLI in C, importazione di manoscritti, esportazione EPUB e anteprime di stampa. Il progetto rimane composto da normali file Markdown e `book.yaml`.

> **Software alfa:** conserva backup e lavora su copie. Controlla ogni importazione/esportazione. Un file generato non è automaticamente certificato per Amazon KDP, IngramSpark o altri distributori.

[![CI](https://github.com/massigent/PubCraft/actions/workflows/ci.yml/badge.svg)](https://github.com/massigent/PubCraft/actions/workflows/ci.yml)
[![Licenza MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**Ultima versione: 1.0.0-alpha.3 — app Mac universale, nativa su Intel e Apple Silicon.**

### Download

| Sistema | Download |
| --- | --- |
| **macOS Universal (Intel + Apple Silicon)** | **[ZIP di PubCraft.app](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-1.0.0-alpha.3-macos-universal.app.zip)** · [App DMG](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-1.0.0-alpha.3.dmg) · [Installer PKG](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-1.0.0-alpha.3.pkg) · [Strumenti portatili](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-1.0.0-alpha.3-macos.tar.gz) |
| Windows x64 | [Installer](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-Setup-1.0.0-alpha.3.exe) · [ZIP portatile](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-1.0.0-alpha.3-windows-x64.zip) |
| Linux x64 | [AppImage](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-1.0.0-alpha.3-x86_64.AppImage) · [Debian/Ubuntu](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-1.0.0-alpha.3-linux-amd64.deb) · [Archivio portatile](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/PubCraft-1.0.0-alpha.3-linux-x64.tar.gz) |

[Note e tutti i file](https://github.com/massigent/PubCraft/releases/tag/v1.0.0-alpha.3) · [Checksum SHA-256](https://github.com/massigent/PubCraft/releases/download/v1.0.0-alpha.3/checksums-sha256.txt) · [English](README.md) · [Segnala un problema](https://github.com/massigent/PubCraft/issues/new?template=bug_report.md)

**Installazione Mac:** estrai il ZIP e trascina `PubCraft.app` in Applicazioni, oppure usa il DMG come prima. **Richiede macOS 13 Ventura o successivo; Rosetta non serve.** Launcher, compilatore, GUI e helper PDF contengono entrambi i tipi di processore. Chiudi la vecchia app prima di sostituirla.

L'app ha una firma ad-hoc, non una firma Developer ID/notarizzazione Apple; PKG e installer Windows non sono firmati da un editore verificato. Il sistema può mostrare avvisi di sicurezza. Usa la Release ufficiale e verifica i checksum. Gli archivi “Source code” contengono sorgenti, non app pronte.

![Editor PubCraft e anteprima del libro](docs/media/pubcraft-editor.png)

## Novità

**Alfa 3:** `PubCraft.app` e strumenti Mac realmente Universal 2; ZIP dell'app; launcher nativo con requisito macOS controllato; verifiche automatiche di architetture, firme, permessi, versioni e commit di origine. Gli stessi pacchetti vengono provati su Intel e Apple Silicon. Le [istruzioni per gli agenti](AGENTS.md) e la [guida di rilascio](docs/RELEASING.md) rendono esplicito che non si deve più confondere una build Intel-only con una universale.

**Incluse dall'alfa 2:** schede Corkboard, sinossi, stato dei capitoli, conteggi selezione/sessione, palette comandi, import Scrivener e miglioramenti DOCX/ODT/Markdown. Selezione e salvataggi più sicuri, protezione di metadati/copertine/cestino, sanitizzazione XML/UTF-8, immagini/liste annidate e identificatori persistenti.

Consulta [note alfa 3](RELEASE_NOTES_1.0.0-alpha.3.md), [modifiche complete alfa 2](RELEASE_NOTES_1.0.0-alpha.2.md), [changelog](CHANGELOG.md) e [validazione](docs/RELEASE_VALIDATION_1.0.0-alpha.3.md).

## Perché PubCraft?

- **Progetti portabili:** capitoli Markdown, YAML e immagini locali, modificabili anche con altri editor.
- **Lavoro locale:** nessun account/backend cloud PubCraft. Eventuali client AI esterni collegati tramite MCP hanno le proprie politiche di riservatezza.
- **Importazioni:** Vellum, Scrivener, DOCX, ODT, Markdown e PDF. La conversione non riproduce perfettamente ogni caratteristica originale: verifica il risultato.
- **Scrittura e pubblicazione:** capitoli, note, anteprima del manoscritto, tipografia e anteprime reali EPUB/PDF generate su richiesta.
- **Stampa:** HTML/PDF con formato e abbondanza; serve un renderer supportato e la resa può variare.
- **Gratuito e open source:** codice MIT; font/dipendenze conservano le rispettive licenze.

## Iniziare

Avvia l'app e scegli **Nuovo libro** o una cartella progetto. Il libro contiene `book.yaml`, capitoli `.md`/`.markdown` e facoltativamente `cover/` e `images/`. Conserva originale e backup separato.

```sh
# GUI e Watch
pubcraftgui ./mio-libro
pubcraftgui --watch ./mio-libro

# CLI
pubcraft init ./mio-libro "Il mio libro"
pubcraft build ./mio-libro ./mio-libro.epub --kindle
pubcraft build-print ./mio-libro ./out-stampa --trim 6x9 --pages 200 --pdf

# Conversione Vellum
pubcraft export-md "./Il mio libro.vellum" "./Il mio libro-markdown"
```

Il browser viene servito da un processo locale, non da un servizio ospitato. Gli altri formati sono importabili dalla GUI; il binario include MCP su stdio con `pubcraftgui --mcp`.

## Compilazione e test

Minimo sorgenti: `make`, compilatore C, Go 1.23+ e Python. Le release usano il Go fissato in `.go-version`; il packaging Universal richiede SDK/strumenti di firma macOS e Python 3.12+.

```sh
make                       # sviluppo nativo, NON release Mac
make check                 # CLI, Go -race senza cache e test dei controlli release
go -C src/pubcraftgui vet ./...
make pkg-macos             # app Universal + ZIP/DMG/PKG/strumenti portatili
make verify-macos-release  # formati, architetture, firme e commit pulito
```

Prima di modificare/pubblicare release leggi [AGENTS.md](AGENTS.md) e [docs/RELEASING.md](docs/RELEASING.md). Non riusare vecchi binari e non sostituire l'app universale con gli output nativi di `make all`. Il workflow [Release builds](.github/workflows/release-build.yml) contiene i controlli nativi Intel/Apple Silicon, Linux e Windows. Per i test browser vedi [tests/README.md](tests/README.md). Una cross-compilazione non equivale a test di esecuzione/installazione.

## Limiti e collaborazione

Importazioni, tipografia e paginazione dipendono da contenuti, font, sistema e renderer; i lettori EPUB possono sostituire la tipografia. L'helper WebKit produce uno snapshot continuo, non un impaginato KDP multipagina certificato. Le forme YAML non aggiornabili vengono rifiutate senza alterare il progetto. Non sono inclusi pacchetti Linux ARM.

Sono benvenuti [segnalazioni](https://github.com/massigent/PubCraft/issues), documentazione, traduzioni e test: [CONTRIBUTING.md](CONTRIBUTING.md). Indica versione, sistema e passaggi. **Non allegare manoscritti privati o copertine protette. Leggi i rapporti diagnostici prima di pubblicarli: la redazione automatica non garantisce di rimuovere ogni dato sensibile.**

## Licenza

Codice [MIT](LICENSE). Vedi [font](fonts/README.md) e [licenze di terze parti](third-party-licenses/).
