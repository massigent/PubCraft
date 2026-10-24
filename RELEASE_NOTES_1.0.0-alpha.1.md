# PubCraft 1.0.0-alpha.1

PubCraft is an early, free and open-source tool for writing and publishing books from portable Markdown projects. This first public alpha invites writers and contributors to try the workflow and report problems.

## Downloads

- **macOS Intel (x86_64):** `.dmg`, `.pkg`, or manual `.tar.gz`
- **Windows x64:** portable `.zip` or installer `.exe`
- **Linux x64:** `.tar.gz`, Debian/Ubuntu `.deb`, or `.AppImage`
- `checksums-sha256.txt`: SHA-256 checksums for the release assets

Apple Silicon Macs may require Rosetta. The macOS app and Windows installer are not signed/notarized; your operating system may show a security warning. Download only from this GitHub release and review the checksums.

## What to try

- Create or open a Markdown book project and edit chapters in the local GUI.
- Import a supported Vellum project and check the resulting chapter order, images, metadata, and formatting.
- Export an EPUB and a print-layout PDF, then inspect both with your intended distributor's previewer.
- Tell us which operating system you used, what you expected, and what happened. See [Contributing](https://github.com/massigent/PubCraft/blob/main/CONTRIBUTING.md).

## Important alpha notes

- Keep your original manuscript and a separate backup; work on a copy when evaluating the app.
- Vellum import is a conversion, not a perfect reproduction of every layout feature.
- PDF generation depends on the available renderer. An export completing successfully does not certify KDP, IngramSpark, or other print-service compliance.
- Windows x64 packages are included, but this release was assembled from macOS/Linux tooling and was not runtime-tested on a native Windows host during packaging. Please report installation/startup results.
- Native Apple Silicon and Linux ARM packages are not included in this release.

## Validation performed for this release

- `make check` (CLI and Go tests with the race detector): passed.
- Full browser regression suite, including real PDF rendering through the dedicated test adapter: passed on this build.
- Portable macOS/Linux/Windows archive contents and binary freshness: checked with `tests/check_packages.py`.
- macOS DMG checksum verified; Linux CLI smoke test and AppImage extraction smoke test passed.
- Windows installer was generated and identified as an NSIS x64 installer; native Windows execution was not available in the packaging environment.

Report issues with a minimal synthetic project. Do not attach unpublished writing, private manuscripts, or copyrighted covers.

---

## Italiano

PubCraft è uno strumento gratuito e open source in fase iniziale per scrivere e pubblicare libri usando progetti Markdown portabili. Questa prima alfa pubblica invita autori e collaboratori a provare il flusso e segnalare problemi.

### Note importanti

- Conserva il manoscritto originale e un backup separato; per provare l'app usa una copia.
- L'importazione Vellum converte i contenuti, ma non riproduce necessariamente ogni dettaglio di impaginazione.
- La generazione PDF dipende dal renderer disponibile. Un'esportazione completata non certifica la conformità a KDP, IngramSpark o altri servizi.
- Sono inclusi i pacchetti Windows x64, ma durante la preparazione non è stato possibile provarli su un host Windows nativo. Sono gradite segnalazioni su installazione e avvio.
- Non sono inclusi pacchetti nativi Apple Silicon o Linux ARM.

Segnala i problemi con un progetto sintetico e privo di dati personali. Non allegare testi inediti, manoscritti privati o copertine protette da copyright.
