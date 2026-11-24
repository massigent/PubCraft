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

# -----------------------------------------------------------------------------
# Test 12: indice automatico — config `toc:` dopo `chapters:` e --no-toc-pagenumbers
# -----------------------------------------------------------------------------
echo "[Test 12] Verifica indice automatico (toc: dopo chapters, sottocapi, flag numeri)..."
mkdir -p "$TMP_DIR/v12"
cat <<EOF > "$TMP_DIR/v12/book.yaml"
title: "Indice Test"
author: "Autore"
language: "it"
chapters:
  - file: "01.md"
    id: "c1"
    title: "Capitolo Uno"
toc:
  subheads: true
  pagenumbers: false
EOF
cat <<EOF > "$TMP_DIR/v12/01.md"
Testo introduttivo.

## Prima Sezione

Altro testo.

## Seconda Sezione

Fine.
EOF
# il blocco toc: sta dopo chapters: — prima veniva ignorato in silenzio
INFO_OUT=$("$BIN" info "$TMP_DIR/v12")
case "$INFO_OUT" in
    *"toc:        subheads=true pagenumbers=false"*) ;;
    *)
        echo "FALLITO: opzioni toc: dopo chapters: non applicate." >&2
        echo "$INFO_OUT" >&2
        exit 1
        ;;
esac
"$BIN" build-print "$TMP_DIR/v12" "$TMP_DIR/v12/out" --trim 6x9 --pages 100 >/dev/null
if ! grep -q 'href="#ch1-s1"' "$TMP_DIR/v12/out/print.html"; then
    echo "FALLITO: i sottocapi non compaiono nell'indice di stampa." >&2
    exit 1
fi
if grep -q 'class="toc-list"' "$TMP_DIR/v12/out/print.html"; then
    echo "FALLITO: indice con numeri di pagina nonostante toc.pagenumbers=false (find: class=\"toc-list\" presente)." >&2
    exit 1
fi
# --no-toc-pagenumbers: nessun marcatore nascosto, indice senza numeri
cat <<EOF > "$TMP_DIR/v12/book.yaml"
title: "Indice Test"
author: "Autore"
language: "it"
toc:
  subheads: true
chapters:
  - file: "01.md"
    id: "c1"
    title: "Capitolo Uno"
EOF
"$BIN" build-print "$TMP_DIR/v12" "$TMP_DIR/v12/out2" --trim 6x9 --pages 100 --no-toc-pagenumbers >/dev/null
if grep -q 'PCMAP-' "$TMP_DIR/v12/out2/print.html"; then
    echo "FALLITO: --no-toc-pagenumbers ha comunque emesso i marcatori nascosti." >&2
    exit 1
fi
if grep -q '<div class="toc-p">' "$TMP_DIR/v12/out2/print.html"; then
    echo "FALLITO: --no-toc-pagenumbers ha comunque emesso la colonna dei numeri." >&2
    exit 1
fi
echo "  ✔ toc: applicato anche dopo chapters:, sottocapi nell'indice e --no-toc-pagenumbers senza marcatori."

# -----------------------------------------------------------------------------
# Test 13: tipi editoriali — colophon come alias di copyright, tipi sconosciuti
# -----------------------------------------------------------------------------
echo "[Test 13] Verifica tipo colophon (alias copyright) e tipi sconosciuti..."
mkdir -p "$TMP_DIR/v13"
cat <<EOF > "$TMP_DIR/v13/book.yaml"
title: "Tipi"
author: "Autore"
language: "it"
chapters:
  - file: "copy.md"
    id: "copy"
    title: "Copyright"
    type: "copyright"
  - file: "col.md"
    id: "col"
    title: "Colophon"
    type: "colophon"
  - file: "c1.md"
    id: "c1"
    title: "Capitolo Uno"
    type: "chapter"
  - file: "odd.md"
    id: "odd"
    title: "Strano"
    type: "parties"
EOF
printf 'Copyright 2026.\n' > "$TMP_DIR/v13/copy.md"
printf 'Prodotto con PubCraft.\n' > "$TMP_DIR/v13/col.md"
printf 'Testo del capitolo.\n' > "$TMP_DIR/v13/c1.md"
printf 'Contenuto con tipo non riconosciuto.\n' > "$TMP_DIR/v13/odd.md"

set +e
"$BIN" build-print "$TMP_DIR/v13" "$TMP_DIR/v13/out" --trim 6x9 --pages 100 >"$TMP_DIR/v13/print.log" 2>"$TMP_DIR/v13/print.err"
RC_TYPES=$?
set -e
if [ "$RC_TYPES" -ne 0 ]; then
    echo "FALLITO: un tipo sconosciuto non deve interrompere la build." >&2
    cat "$TMP_DIR/v13/print.err" >&2
    exit 1
fi
if ! grep -q "tipo di sezione 'parties' non riconosciuto" "$TMP_DIR/v13/print.err"; then
    echo "FALLITO: manca l'avviso sul tipo sconosciuto 'parties'." >&2
    cat "$TMP_DIR/v13/print.err" >&2
    exit 1
fi
# il capolettera spezza la prima lettera: cerca il resto della frase
if ! grep -q 'on tenuto con tipo non riconosciuto\|on<span class="dropcap">.*</span>tenuto con tipo non riconosciuto\|tenuto con tipo non riconosciuto' "$TMP_DIR/v13/out/print.html"; then
    echo "FALLITO: il contenuto del capitolo con tipo sconosciuto è stato perso." >&2
    exit 1
fi
# colophon = alias di copyright: fuori dall'indice stampato, nessun titolo di capitolo
if grep -q 'href="#ch2">Colophon' "$TMP_DIR/v13/out/print.html"; then
    echo "FALLITO: il colophon compare nell'indice stampato (dovrebbe essere frontmatter come copyright)." >&2
    exit 1
fi
if grep -q '<h2>Colophon</h2>' "$TMP_DIR/v13/out/print.html"; then
    echo "FALLITO: il colophon ha un titolo di capitolo in stampa." >&2
    exit 1
fi
if ! grep -q 'id="ch4">.*Strano\|<h2>Strano</h2>' "$TMP_DIR/v13/out/print.html"; then
    echo "FALLITO: il capitolo con tipo sconosciuto non è impaginato come capitolo normale." >&2
    exit 1
fi
# EPUB: il colophon resta presente nel libro (contenuto intatto)
"$BIN" build "$TMP_DIR/v13" "$TMP_DIR/v13/out.epub" >/dev/null
unzip -p "$TMP_DIR/v13/out.epub" OEBPS/chap002.xhtml | grep -q 'rodotto con PubCraft' \
    || { echo "FALLITO: il colophon non è presente nell'EPUB." >&2; exit 1; }
# alias: nell'EPUB copyright e colophon ricevono lo stesso trattamento (nessuno
# dei due è speciale fuori dalla stampa)
unzip -p "$TMP_DIR/v13/out.epub" OEBPS/chap001.xhtml | grep -q '<h2>Copyright</h2>' \
    || { echo "FALLITO: regressione sul trattamento EPUB di copyright." >&2; exit 1; }
unzip -p "$TMP_DIR/v13/out.epub" OEBPS/chap002.xhtml | grep -q '<h2>Colophon</h2>' \
    || { echo "FALLITO: colophon e copyright non ricevono lo stesso trattamento EPUB." >&2; exit 1; }
echo "  ✔ colophon trattato come copyright (fuori indice stampa, contenuto intatto) e tipo sconosciuto segnalato senza bloccare."

# -----------------------------------------------------------------------------
# Test 14: Markdown nelle voci dell'indice (HTML, stampa, nav EPUB, NCX)
# -----------------------------------------------------------------------------
echo "[Test 14] Verifica resa inline delle voci d'indice e testo puro nell'NCX..."
mkdir -p "$TMP_DIR/v14"
cat <<EOF > "$TMP_DIR/v14/book.yaml"
title: "Indice Inline"
author: "Autore"
language: "it"
toc:
  subheads: on
  pagenumbers: off
chapters:
  - file: "c1.md"
    id: "c1"
    title: "Capitolo & Uno"
    type: "chapter"
EOF
cat <<'EOF' > "$TMP_DIR/v14/c1.md"
Testo.

## Sezione con *corsivo* & "virgolette" <tag>

Altro testo.

## Sezione con [link](https://example.com) e **grassetto**
EOF
"$BIN" build-print "$TMP_DIR/v14" "$TMP_DIR/v14/out" --trim 6x9 --pages 100 >/dev/null
"$BIN" build "$TMP_DIR/v14" "$TMP_DIR/v14/out.epub" >/dev/null
# stampa: corsivo reso, niente delimitatori, escaping corretto
grep -q '<em>corsivo</em>' "$TMP_DIR/v14/out/print.html" \
    || { echo "FALLITO: il corsivo non è reso nell'indice di stampa." >&2; exit 1; }
grep -q '&amp; &quot;virgolette&quot; &lt;tag&gt;' "$TMP_DIR/v14/out/print.html" \
    || { echo "FALLITO: escaping errato nell'indice di stampa." >&2; exit 1; }
grep -q '\*corsivo\*' "$TMP_DIR/v14/out/print.html" \
    && { echo "FALLITO: delimitatori Markdown visibili nell'indice di stampa." >&2; exit 1; }
# nessun link annidato nella voce d'indice: il titolo del link resta testo
if grep -q 'class="toc"' "$TMP_DIR/v14/out/print.html"; then
    TOC_FRAG=$(sed -n '/class="toc"/,/<\/div>$/p' "$TMP_DIR/v14/out/print.html")
    printf '%s' "$TOC_FRAG" | grep -q 'https://example.com' \
        && { echo "FALLITO: link annidato nell'indice di stampa." >&2; exit 1; }
fi
grep -q '<strong>grassetto</strong>' "$TMP_DIR/v14/out/print.html" \
    || { echo "FALLITO: il grassetto non è reso nell'indice." >&2; exit 1; }
# nav EPUB: resa inline, niente link annidati
unzip -p "$TMP_DIR/v14/out.epub" OEBPS/nav.xhtml > "$TMP_DIR/v14/nav.xhtml"
grep -q '<em>corsivo</em>' "$TMP_DIR/v14/nav.xhtml" \
    || { echo "FALLITO: il corsivo non è reso nella nav EPUB." >&2; exit 1; }
grep -q 'https://example.com' "$TMP_DIR/v14/nav.xhtml" \
    && { echo "FALLITO: link annidato nella nav EPUB." >&2; exit 1; }
# NCX: testo puro, senza delimitatori né tag
unzip -p "$TMP_DIR/v14/out.epub" OEBPS/toc.ncx > "$TMP_DIR/v14/toc.ncx"
grep -q 'Sezione con corsivo &amp; &quot;virgolette&quot; &lt;tag&gt;' "$TMP_DIR/v14/toc.ncx" \
    || { echo "FALLITO: testo NCX non normalizzato: $(grep -o 'Sezione[^<]*' "$TMP_DIR/v14/toc.ncx" | head -2 | tr '\n' '|')" >&2; exit 1; }
grep -qE '\*|<em>|<strong>' "$TMP_DIR/v14/toc.ncx" \
    && { echo "FALLITO: l'NCX contiene delimitatori Markdown o tag." >&2; exit 1; }
echo "  ✔ voci d'indice con resa inline (stampa, EPUB nav) e testo puro nell'NCX."

# -----------------------------------------------------------------------------
# Test 15: valori booleani di toc: (on/off, null, non valido)
# -----------------------------------------------------------------------------
echo "[Test 15] Verifica valori booleani di toc: in book.yaml..."
mkdir -p "$TMP_DIR/v15"
printf 'Testo.\n\n## Sezione\n' > "$TMP_DIR/v15/c1.md"
cat <<EOF > "$TMP_DIR/v15/book.yaml"
title: "Bool"
author: "Autore"
language: "it"
toc:
  subheads: on
  pagenumbers: null
chapters:
  - file: "c1.md"
    id: "c1"
    title: "Uno"
EOF
"$BIN" build-print "$TMP_DIR/v15" "$TMP_DIR/v15/out" --trim 6x9 --pages 100 >/dev/null
grep -q 'href="#ch1-s1"' "$TMP_DIR/v15/out/print.html" \
    || { echo "FALLITO: subheads: on non applicato." >&2; exit 1; }
cat <<EOF > "$TMP_DIR/v15/book.yaml"
title: "Bool"
author: "Autore"
language: "it"
toc:
  subheads: forse
chapters:
  - file: "c1.md"
    id: "c1"
    title: "Uno"
EOF
set +e
"$BIN" build-print "$TMP_DIR/v15" "$TMP_DIR/v15/out-bad" --trim 6x9 --pages 100 >/dev/null 2>"$TMP_DIR/v15/bad.err"
RC_BAD=$?
set -e
if [ "$RC_BAD" -eq 0 ]; then
    echo "FALLITO: un valore toc non valido deve interrompere la build." >&2
    exit 1
fi
grep -q "toc.subheads non valido" "$TMP_DIR/v15/bad.err" \
    || { echo "FALLITO: messaggio d'errore assente o poco chiaro." >&2; cat "$TMP_DIR/v15/bad.err" >&2; exit 1; }
echo "  ✔ on/off e null accettati, valore non valido rifiutato con messaggio esplicito."

echo "=========================================================="
echo " TUTTI I TEST DELLA CLI PUBCRAFT SONO STATI SUPERATI CON SUCCESSO! "
echo "=========================================================="
