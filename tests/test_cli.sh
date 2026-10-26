#!/bin/sh
set -e

BIN="${1:-./build/pubcraft}"
if [ ! -x "$BIN" ]; then
    echo "Errore: binario $BIN non trovato o non eseguibile." >&2
    exit 1
fi

BIN_ABS="$(cd "$(dirname "$BIN")" && pwd)/$(basename "$BIN")"
TMP_DIR=$(mktemp -d /tmp/pubcraft-tests-XXXXXX)
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM

echo "===> Avvio suite di test per PubCraft ($BIN) in $TMP_DIR"

# -----------------------------------------------------------------------------
# Test 1: Protezione da cancellazione/troncamento immagini originali
# -----------------------------------------------------------------------------
echo "[Test 1] Verifica safe_copy_file() contro svuotamento immagini/font..."
mkdir -p "$TMP_DIR/v1/images"
printf "IMAGE_BINARY_DATA_TEST_1234567890_PUB_CRAFT" > "$TMP_DIR/v1/images/copertina.png"
ORIG_SIZE=$(wc -c < "$TMP_DIR/v1/images/copertina.png" | tr -d ' ')

cat <<EOF > "$TMP_DIR/v1/book.yaml"
title: "Libro Test"
author: "Autore Test"
language: "it"
chapters:
  - file: "cap1.md"
    title: "Capitolo Uno"
EOF

cat <<EOF > "$TMP_DIR/v1/cap1.md"
# Capitolo Uno
![Copertina](images/copertina.png)
Testo del capitolo.
EOF

# Esporta EPUB e Print verso cartella che include la sorgente o file identico
"$BIN" build "$TMP_DIR/v1" "$TMP_DIR/v1/out.epub"
NEW_SIZE=$(wc -c < "$TMP_DIR/v1/images/copertina.png" | tr -d ' ')
if [ "$NEW_SIZE" -ne "$ORIG_SIZE" ]; then
    echo "FALLITO: Immagine originale modificata o svuotata dopo build EPUB ($ORIG_SIZE -> $NEW_SIZE byte)!" >&2
    exit 1
fi

# Test critico: build-print con destinazione coincidente con la cartella vault
"$BIN" build-print "$TMP_DIR/v1" "$TMP_DIR/v1"
NEW_SIZE=$(wc -c < "$TMP_DIR/v1/images/copertina.png" | tr -d ' ')
if [ "$NEW_SIZE" -ne "$ORIG_SIZE" ]; then
    echo "FALLITO: Immagine originale modificata o svuotata dopo build-print su vault sorgente ($ORIG_SIZE -> $NEW_SIZE byte)!" >&2
    exit 1
fi
echo "  ✔ Immagine originale intatta ($NEW_SIZE byte) sia su EPUB sia su build-print nello stesso vault."

# -----------------------------------------------------------------------------
# Test 2: Capitoli mancanti devono interrompere la build con codice d'uscita != 0
# -----------------------------------------------------------------------------
echo "[Test 2] Verifica errore fatale su capitolo mancante..."
mkdir -p "$TMP_DIR/v2"
cat <<EOF > "$TMP_DIR/v2/book.yaml"
title: "Libro Incompleto"
author: "Autore"
chapters:
  - file: "inesistente.md"
    title: "Fantasma"
EOF

set +e
"$BIN" build "$TMP_DIR/v2" "$TMP_DIR/v2/test.epub" >/dev/null 2>&1
RC=$?
set -e

if [ "$RC" -eq 0 ]; then
    echo "FALLITO: pubcraft build ha restituito 0 nonostante capitolo mancante!" >&2
    exit 1
fi
echo "  ✔ Capitolo mancante intercetta errore con exit code $RC."

# -----------------------------------------------------------------------------
# Test 3: EPUB con percorso relativo non scompare dopo cd
# -----------------------------------------------------------------------------
echo "[Test 3] Verifica generazione EPUB con percorso relativo..."
mkdir -p "$TMP_DIR/v3"
cat <<EOF > "$TMP_DIR/v3/book.yaml"
title: "Percorso Relativo"
author: "Autore"
chapters:
  - file: "cap1.md"
    title: "Inizio"
EOF
echo "Testo del capitolo." > "$TMP_DIR/v3/cap1.md"

CUR_PWD="$PWD"
cd "$TMP_DIR"
"$BIN_ABS" build "v3" "output_relativo.epub"
if [ ! -s "output_relativo.epub" ]; then
    echo "FALLITO: output_relativo.epub non esiste o è vuoto!" >&2
    exit 1
fi
echo "  ✔ EPUB relativo creato correttamente ($(wc -c < output_relativo.epub | tr -d ' ') byte)."
cd "$CUR_PWD"

# -----------------------------------------------------------------------------
# Test 4: Validità EPUB 3 (nav.xhtml, properties="nav", XML escaping)
# -----------------------------------------------------------------------------
echo "[Test 4] Verifica EPUB 3 (nav.xhtml, content.opf, escaping XML)..."
mkdir -p "$TMP_DIR/v4"
cat <<EOF > "$TMP_DIR/v4/book.yaml"
title: "Titolo & Sottotitolo <Special>"
author: "Autore & Co."
language: "it"
chapters:
  - file: "cap1.md"
    title: "Capitolo A & B"
EOF
cat <<EOF > "$TMP_DIR/v4/cap1.md"
# Capitolo A & B
Un paragrafo con **grassetto**, *corsivo*, e un confronto: 5 < 10 & 10 > 2.
EOF

"$BIN" build "$TMP_DIR/v4" "$TMP_DIR/v4/test.epub"

# Verifica nav.xhtml
if ! unzip -l "$TMP_DIR/v4/test.epub" | grep -q "OEBPS/nav.xhtml"; then
    echo "FALLITO: OEBPS/nav.xhtml mancante nell'archivio EPUB!" >&2
    exit 1
fi

# Verifica dichiarazione properties="nav" in content.opf
OPF_CONTENT=$(unzip -p "$TMP_DIR/v4/test.epub" "OEBPS/content.opf")
echo "$OPF_CONTENT" | grep -q 'properties="nav"' || {
    echo "FALLITO: properties=\"nav\" non trovato in content.opf!" >&2
    exit 1
}

# Verifica XML escaping nei titoli e nel testo
echo "$OPF_CONTENT" | grep -q "Titolo &amp; Sottotitolo &lt;Special&gt;" || {
    echo "FALLITO: Escaping XML mancante nel titolo in content.opf!" >&2
    exit 1
}

NAV_CONTENT=$(unzip -p "$TMP_DIR/v4/test.epub" "OEBPS/nav.xhtml")
echo "$NAV_CONTENT" | grep -q "Capitolo A &amp; B" || {
    echo "FALLITO: Escaping XML mancante in nav.xhtml!" >&2
    exit 1
}
echo "  ✔ EPUB 3 nav.xhtml, OPF e XML escaping conformi."

# -----------------------------------------------------------------------------
# Test 5: Precisione abbondanza stampa (3 decimali) e Markdown inline
# -----------------------------------------------------------------------------
echo "[Test 5] Verifica precisione bleed 3 decimali (6.125in) e formattazione Markdown..."
mkdir -p "$TMP_DIR/v5"
cat <<EOF > "$TMP_DIR/v5/book.yaml"
title: "Stampa Test"
author: "Scrittore"
style: "classic"
opening: "dropcap"
chapters:
  - file: "cap1.md"
    title: "Primo Capitolo"
EOF

cat <<EOF > "$TMP_DIR/v5/cap1.md"
# Primo Capitolo

C'era una volta un re che amava il **grassetto** e il *corsivo*.

### Un Sottotitolo H3

> Questa è una citazione a blocchi importante.

- Elemento lista uno
- Elemento lista due

---

Fine del capitolo con [un link](https://pubcraft.org).
EOF

"$BIN" build-print "$TMP_DIR/v5" "$TMP_DIR/v5/out-print" --trim 6x9 --bleed --pages 150

HTML_PRINT="$TMP_DIR/v5/out-print/print.html"
if [ ! -f "$HTML_PRINT" ]; then
    echo "FALLITO: print.html non generato!" >&2
    exit 1
fi

# Verifica 3 decimali per bleed (6.125in x 9.250in)
grep -q "size: 6.125in 9.250in;" "$HTML_PRINT" || {
    echo "FALLITO: Dimensione pagina con bleed errata! Atteso 'size: 6.125in 9.250in;'." >&2
    grep "size:" "$HTML_PRINT" >&2
    exit 1
}

# Verifica parsing Markdown inline in C
grep -q "<strong>grassetto</strong>" "$HTML_PRINT" || {
    echo "FALLITO: **grassetto** non convertito in <strong>grassetto</strong>!" >&2
    exit 1
}
grep -q "<em>corsivo</em>" "$HTML_PRINT" || {
    echo "FALLITO: *corsivo* non convertito in <em>corsivo</em>!" >&2
    exit 1
}
grep -q "<h4" "$HTML_PRINT" || {
    echo "FALLITO: ### Sottotitolo non convertito in <h4!" >&2
    exit 1
}
grep -q "<blockquote>" "$HTML_PRINT" || {
    echo "FALLITO: > Citazione non convertita in <blockquote>!" >&2
    exit 1
}
grep -q "<li>" "$HTML_PRINT" || {
    echo "FALLITO: - Lista non convertita in <li>!" >&2
    exit 1
}
grep -q '<hr class="sb"/>' "$HTML_PRINT" || {
    echo "FALLITO: --- interruzione scena non convertita in <hr class=\"sb\"/>!" >&2
    exit 1
}
grep -q '<a href="https://pubcraft.org">un link</a>' "$HTML_PRINT" || {
    echo "FALLITO: [link](url) non convertito in tag <a>!" >&2
    exit 1
}

echo "  ✔ Bleed 6.125x9.250 in e Markdown inline completi e corretti."

# -----------------------------------------------------------------------------
# Test 6: Parsing YAML indentato e strip virgolette
# -----------------------------------------------------------------------------
echo "[Test 6] Verifica parsing YAML con indentazioni e virgolette..."
mkdir -p "$TMP_DIR/v6"
cat <<EOF > "$TMP_DIR/v6/book.yaml"
  title: "Titolo con Virgolette"
  author: 'Autore con Apici'
  language: "it"
  chapters:
    - file: "cap1.md"
      title: "Capitolo Indentato"
EOF
echo "Testo." > "$TMP_DIR/v6/cap1.md"

"$BIN" build "$TMP_DIR/v6" "$TMP_DIR/v6/test.epub"
OPF_V6=$(unzip -p "$TMP_DIR/v6/test.epub" "OEBPS/content.opf")

if echo "$OPF_V6" | grep -q '"Titolo con Virgolette"'; then
    echo "FALLITO: Le virgolette non sono state rimosse dal titolo YAML!" >&2
    exit 1
fi
echo "$OPF_V6" | grep -q "<dc:title>Titolo con Virgolette</dc:title>" || {
    echo "FALLITO: Titolo non trovato in content.opf!" >&2
    exit 1
}
echo "$OPF_V6" | grep -q "<dc:creator>Autore con Apici</dc:creator>" || {
    echo "FALLITO: Autore con apici rimossi non trovato in content.opf!" >&2
    exit 1
}
echo "  ✔ YAML indentato e pulizia virgolette convalidati."

# -----------------------------------------------------------------------------
# Test 7: Risoluzione font e copia per libro con font: ebgaramond
# -----------------------------------------------------------------------------
echo "[Test 7] Verifica risoluzione font EB Garamond e generazione CSS corretta..."
mkdir -p "$TMP_DIR/v7"
cat <<EOF > "$TMP_DIR/v7/book.yaml"
title: "Libro Font Garamond"
author: "Autore"
language: "it"
font: ebgaramond
chapters:
  - file: "cap1.md"
    title: "Capitolo Uno"
EOF

cat <<EOF > "$TMP_DIR/v7/cap1.md"
# Capitolo Uno
Testo con font EB Garamond.
EOF

"$BIN" build-print "$TMP_DIR/v7" "$TMP_DIR/v7/out-print"
if [ ! -f "$TMP_DIR/v7/out-print/fonts/ebgaramond-400.woff2" ]; then
    echo "FALLITO: out-print/fonts/ebgaramond-400.woff2 non trovato!" >&2
    exit 1
fi
if ! grep -q "EB Garamond" "$TMP_DIR/v7/out-print/print.html"; then
    echo "FALLITO: @font-face per EB Garamond mancante in print.html!" >&2
    exit 1
fi
if grep -q "; *!important;" "$TMP_DIR/v7/out-print/print.html"; then
    echo "FALLITO: Errore sintassi CSS (; !important;) in print.html!" >&2
    exit 1
fi
echo "  ✔ Font EB Garamond risolto, copiato e dichiarato correttamente nel CSS di stampa."

echo "[Test 8] Font della CLI installata, indipendenti dalla working directory..."
mkdir -p "$TMP_DIR/prefix/bin" "$TMP_DIR/prefix/share/pubcraft/fonts" "$TMP_DIR/empty-cwd"
cp "$BIN_ABS" "$TMP_DIR/prefix/bin/pubcraft"
cp -R fonts/. "$TMP_DIR/prefix/share/pubcraft/fonts/"
(cd "$TMP_DIR/empty-cwd" && "$TMP_DIR/prefix/bin/pubcraft" build-print "$TMP_DIR/v7" "$TMP_DIR/installed-out")
test -s "$TMP_DIR/installed-out/fonts/ebgaramond-400.woff2"
grep -q "font-family: 'EB Garamond';" "$TMP_DIR/installed-out/print.html"
echo "  ✔ CLI installata: font trovati accanto al prefisso di installazione."

# -----------------------------------------------------------------------------
# Test 9: Verifica tabelle Markdown (print.html e EPUB)
# -----------------------------------------------------------------------------
echo "[Test 9] Verifica tabelle Markdown in EPUB e stampa..."
mkdir -p "$TMP_DIR/v9"
cat <<EOF > "$TMP_DIR/v9/book.yaml"
title: "Libro con Tabelle"
author: "Autore"
language: "it"
chapters:
  - file: "cap1.md"
    title: "Capitolo con Tabella"
EOF
cat <<EOF > "$TMP_DIR/v9/cap1.md"
# Capitolo con Tabella

Testo introduttivo.

| Prodotto | Prezzo | Allineamento |
| :--- | :---: | ---: |
| Libro **Speciale** | 19,90 € | Centro |
| Ebook *Rapido* | 4,99 € | Destra |

Testo di chiusura.
EOF

"$BIN" build "$TMP_DIR/v9" "$TMP_DIR/v9/out.epub"
"$BIN" build-print "$TMP_DIR/v9" "$TMP_DIR/v9/out-print"

if ! grep -q '<table class="book-table">' "$TMP_DIR/v9/out-print/print.html"; then
    echo "FALLITO: Tag <table class=\"book-table\"> mancante in print.html!" >&2
    exit 1
fi
if ! grep -q 'style="text-align:center;">Prezzo</th>' "$TMP_DIR/v9/out-print/print.html"; then
    echo "FALLITO: Allineamento intestazione tabella errato in print.html!" >&2
    exit 1
fi
if ! grep -q 'Libro <strong>Speciale</strong>' "$TMP_DIR/v9/out-print/print.html"; then
    echo "FALLITO: Formattazione inline in cella tabella non resa in print.html!" >&2
    exit 1
fi

EPUB_CH1=$(unzip -p "$TMP_DIR/v9/out.epub" "OEBPS/chap001.xhtml")
if ! echo "$EPUB_CH1" | grep -q '<table class="book-table">'; then
    echo "FALLITO: Tag <table class=\"book-table\"> mancante in EPUB chap001.xhtml!" >&2
    exit 1
fi
echo "  ✔ Tabelle Markdown compilate correttamente in EPUB e stampa con allineamenti e stili inline."

# -----------------------------------------------------------------------------
# Test 10: pubcraft init con titolo contenente due punti e caratteri speciali
# -----------------------------------------------------------------------------
echo "[Test 10] Verifica pubcraft init con quoting corretto del titolo in YAML..."
"$BIN" init "$TMP_DIR/v10" "Libro: sottotitolo speciale"
if ! grep -q 'title: "Libro: sottotitolo speciale"' "$TMP_DIR/v10/book.yaml"; then
    echo "FALLITO: Titolo non quotato correttamente in book.yaml!" >&2
    exit 1
fi
INFO_OUT=$("$BIN" info "$TMP_DIR/v10")
if ! echo "$INFO_OUT" | grep -qE "titolo:\s+Libro: sottotitolo speciale"; then
    echo "FALLITO: pubcraft info non ha estratto correttamente il titolo con due punti!" >&2
    exit 1
fi
echo "  ✔ pubcraft init ha quotato correttamente il titolo e pubcraft info lo ha interpretato con successo."

# -----------------------------------------------------------------------------
# Test 11: pubcraft init non sovrascrive un 01-capitolo-1.md preesistente
# -----------------------------------------------------------------------------
echo "[Test 11] Verifica protezione da sovrascrittura di capitoli preesistenti in pubcraft init..."
mkdir -p "$TMP_DIR/v11"
printf "MANUSCRITTO_DA_PROTEGGERE" > "$TMP_DIR/v11/01-capitolo-1.md"
set +e
"$BIN" init "$TMP_DIR/v11" "Tentativo Sovrascrittura" >/dev/null 2>&1
RC_INIT=$?
set -e
if [ "$RC_INIT" -eq 0 ]; then
    echo "FALLITO: pubcraft init è uscito con 0 invece di rifiutare la sovrascrittura del capitolo preesistente!" >&2
    exit 1
fi
REMAINING_CONTENT=$(cat "$TMP_DIR/v11/01-capitolo-1.md")
if [ "$REMAINING_CONTENT" != "MANUSCRITTO_DA_PROTEGGERE" ]; then
    echo "FALLITO: 01-capitolo-1.md è stato corrotto o sovrascritto!" >&2
    exit 1
fi
echo "  ✔ pubcraft init ha bloccato la sovrascrittura del manoscritto restituendo codice d'uscita diverso da 0."

echo "=========================================================="
echo " TUTTI I TEST DELLA CLI PUBCRAFT SONO STATI SUPERATI CON SUCCESSO! "
echo "=========================================================="
