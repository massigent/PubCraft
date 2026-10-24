# PubCraft

**Scrivi il tuo libro in Markdown. Mantieni i file sul tuo computer. Esporta EPUB e impaginati di stampa da un unico progetto.**

PubCraft è uno strumento gratuito e open source per scrivere e pubblicare libri. Offre un editor locale, un'interfaccia a riga di comando, l'esportazione EPUB, strumenti per l'impaginazione di stampa e il supporto all'importazione di progetti Vellum.

> **Software alfa:** lavora su copie dei manoscritti e conserva sempre un backup. Controlla i progetti importati e tutti i file esportati prima della pubblicazione. Gli output per la stampa e gli ebook non sono certificati da Amazon KDP o da altri distributori.

[![CI](https://github.com/massigent/PubCraft/actions/workflows/ci.yml/badge.svg)](https://github.com/massigent/PubCraft/actions/workflows/ci.yml)
[![Licenza MIT](https://img.shields.io/badge/licenza-MIT-blue.svg)](LICENSE)

![PubCraft mostra un capitolo dimostrativo in Markdown accanto all'anteprima tipografica del libro](docs/media/pubcraft-editor.png)

**[Scarica PubCraft](https://github.com/massigent/PubCraft/releases)** · **[Segnala un problema](https://github.com/massigent/PubCraft/issues/new?template=bug_report.md)** · [Collabora](CONTRIBUTING.md) · [English](README.md)

## Perché PubCraft?

Hai un libro in Vellum e vuoi continuare a lavorarci senza dipendere per sempre da quel software? Vuoi scrivere su Windows o Linux, conservare il manoscritto in Markdown o usare uno strumento locale che ti accompagni dalla bozza all'ebook e all'impaginato? PubCraft nasce per rispondere a queste esigenze.

- **File portabili:** capitoli e metadati sono salvati come normali file Markdown e `book.yaml` in una cartella di progetto.
- **Lavoro locale:** PubCraft funziona sul tuo computer e non richiede un account cloud PubCraft. Scegli tu dove conservare i file. Se colleghi un client AI esterno tramite MCP, si applicano le politiche di privacy di quel servizio.
- **Importazione da Vellum:** importa progetti `.vellum` supportati e converte i contenuti in un progetto Markdown modificabile. Non è garantito che ogni dettaglio venga mantenuto: verifica il risultato.
- **Ebook e stampa:** esporta EPUB e genera file per l'impaginazione di stampa con impostazioni per formato pagina e bleed. Per creare il PDF serve un renderer supportato.
- **Il tuo flusso di lavoro:** scrivi nell'editor integrato, usa la CLI o modifica i file Markdown con un altro editor.
- **Gratuito e open source:** il codice di PubCraft usa la licenza MIT. Font e dipendenze incluse hanno licenze proprie: consulta gli avvisi in [third-party-licenses](third-party-licenses/) e le [licenze dei font](fonts/README.md).

## Download

Scarica l'ultima pre-release dalle **[GitHub Releases](https://github.com/massigent/PubCraft/releases)**. Gli asset saranno pubblicati solo dopo essere stati ricompilati e verificati per quella release. Gli archivi “Source code” generati da GitHub non sono programmi di installazione.

Il progetto punta a macOS x86_64, Windows x64 e Linux x64. Gli utenti Apple Silicon potrebbero aver bisogno di Rosetta per l'attuale build macOS; in questa alfa non è inclusa una versione arm64 nativa. Il supporto e il livello di test possono variare tra le versioni alfa: leggi le note della release e scarica solo il pacchetto indicato per il tuo sistema. macOS potrebbe mostrare un avviso relativo a sviluppatore non identificato: le prime build non sono notarizzate. Su Linux l'esportazione PDF può richiedere Chromium o un altro renderer supportato.

## Per iniziare

Un progetto PubCraft è una cartella con `book.yaml` e capitoli Markdown, con eventuali cartelle `cover/` e `images/`.

### Aprire l'editor grafico

```sh
pubcraftgui ./mio-libro
```

L'editor si apre nel browser, ma viene servito da un processo locale sul computer. Per aprire la vista Watch:

```sh
pubcraftgui --watch ./mio-libro
```

### Creare ed esportare dalla riga di comando

```sh
pubcraft init ./mio-libro "Il mio libro"
pubcraft build ./mio-libro ./mio-libro.epub --kindle
pubcraft build-print ./mio-libro ./out-stampa --trim 6x9 --pages 200 --pdf
```

L'esportazione PDF richiede un renderer supportato. Prima del caricamento o della stampa, controlla sempre EPUB e PDF con gli strumenti di anteprima del distributore scelto.

### Importare un progetto Vellum

```sh
pubcraft export-md "./Il mio libro.vellum" "./Il mio libro-markdown"
```

Apri la cartella risultante in PubCraft o in un editor Markdown. L'importazione rende i contenuti più facili da riutilizzare: non riproduce necessariamente ogni funzione di impaginazione di Vellum. Dopo la conversione verifica ordine dei capitoli, immagini, metadati e formattazione. Per modificare il progetto Markdown non è necessario continuare a usare Vellum.

La GUI supporta anche l'importazione di manoscritti `.docx`.

## Compilare dai sorgenti

Requisiti: `make`, un compilatore C e Go 1.23 o successivo. I test browser opzionali richiedono Node.js, le dipendenze di test e Chromium/Brave con remote debugging attivo.

```sh
make
make check
```

Consulta [tests/README.md](tests/README.md) per i test browser e il packaging. I pacchetti d'installazione richiedono strumenti specifici per ciascun sistema operativo; `make dist` non è un cross-compiler universale.

## Limiti dell'alfa

PubCraft è in fase di test attivo. Importazione, paginazione, tipografia e resa PDF possono variare in base ai file originali, ai font, al sistema operativo e al renderer. Un'esportazione completata non certifica la conformità per KDP, IngramSpark o altri servizi. Conserva l'originale e un backup, verifica i file generati con strumenti indipendenti e segnala i problemi usando un esempio minimo privo di testo personale.

## Feedback e collaborazioni

Sono benvenuti feedback, segnalazioni, documentazione, traduzioni, test e contributi al codice: non serve essere sviluppatori. Inizia da [GitHub Issues](https://github.com/massigent/PubCraft/issues) o leggi [CONTRIBUTING.md](CONTRIBUTING.md). Indica versione di PubCraft, sistema operativo, passaggi e risultato atteso/osservato. **Non pubblicare manoscritti privati, testi inediti o copertine protette da copyright.**

PubCraft è nato come strumento per il flusso di scrittura dell'autore. Viene condiviso gratuitamente con la speranza che sia utile ad altri scrittori e che la community aiuti a farlo crescere.

## Licenza

Il codice sorgente di PubCraft è distribuito con licenza [MIT](LICENSE). Font e componenti di terze parti mantengono le rispettive licenze, indicate negli avvisi inclusi nel progetto.
