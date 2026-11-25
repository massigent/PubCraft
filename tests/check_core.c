/* Test interni della CLI: include pubcraft.c con main rinominato e verifica
 * funzioni che non sono esposte da nessun comando pubblico.
 *
 * Contiene anche la verifica della politica degli indici (TOC_POLICY). La
 * matrice è verificata end-to-end su stampa ed EPUB dal test Go
 * TestTocPolicyMatrix; per il formato HTML la copertura end-to-end è parziale
 * (export-html accetta solo archivi e i capitoli degli archivi non portano
 * ancora il tipo di sezione), quindi la colonna HTML è verificata qui,
 * internamente, sui tipi tipizzati. */
#define main original_main
#include "../src/pubcraft.c"
#undef main

static int failures;

#define CHECK(cond, ...)                        \
    do {                                        \
        if (!(cond)) {                          \
            printf("FAIL: " __VA_ARGS__);       \
            printf("\n");                       \
            failures++;                         \
        }                                       \
    } while (0)

static int check_toc_policy(void) {
    /* Matrice canonica: tipo -> incluso nell'indice di stampa.
     * ebook (nav + NCX) e HTML includono tutto, come prima della
     * centralizzazione. */
    static const struct {
        const char *type;
        int in_print;
    } matrix[] = {
        {"chapter", 1},
        {"dedication", 0},
        {"epigraph", 0},
        {"copyright", 0},
        {"about-author", 1},
        {"acknowledgments", 1},
        {"prologue", 1},
        {"epilogue", 1},
        {"preface", 1},
        {"", 1}, /* tipo non dichiarato */
    };
    const size_t n = sizeof matrix / sizeof matrix[0];
    for (size_t i = 0; i < n; i++) {
        const char *t = matrix[i].type;
        CHECK(toc_policy_includes(t, TOC_FMT_PRINT) == matrix[i].in_print,
              "print(%s) atteso %d", t[0] ? t : "(vuoto)", matrix[i].in_print);
        CHECK(toc_policy_includes(t, TOC_FMT_EBOOK) == 1, "ebook(%s) deve essere incluso", t[0] ? t : "(vuoto)");
        CHECK(toc_policy_includes(t, TOC_FMT_HTML) == 1, "html(%s) deve essere incluso", t[0] ? t : "(vuoto)");
        CHECK(toc_policy_is_known(t) == 1, "tipo %s deve essere riconosciuto", t[0] ? t : "(vuoto)");
    }

    /* colophon è un alias di copyright: stessa decisione in ogni formato. */
    for (int fmt = 0; fmt < TOC_FMT_COUNT; fmt++) {
        CHECK(toc_policy_includes("colophon", (TocFormat)fmt) == toc_policy_includes("copyright", (TocFormat)fmt),
              "colophon e copyright devono coincidere nel formato %d", fmt);
    }
    CHECK(toc_policy_is_known("colophon") == 1, "colophon deve essere riconosciuto");
    CHECK(strcmp(toc_policy_canonical("colophon"), "copyright") == 0, "colophon deve normalizzare su copyright");
    CHECK(strcmp(toc_policy_canonical(NULL), "") == 0, "tipo NULL deve normalizzare su stringa vuota");

    /* Tipo sconosciuto: incluso ovunque (comportamento invariato) e segnalato. */
    for (int fmt = 0; fmt < TOC_FMT_COUNT; fmt++)
        CHECK(toc_policy_includes("parties", (TocFormat)fmt) == 1,
              "tipo sconosciuto deve restare incluso nel formato %d", fmt);
    CHECK(toc_policy_is_known("parties") == 0, "tipo sconosciuto non deve risultare noto");

    /* L'elenco stampato nell'avviso è generato dalla stessa tabella: la GUI ha
     * una copia del vocabolario e TestSupportedTypesParity la confronta con
     * questo testo. */
    char *list = NULL;
    size_t list_len = 0;
    FILE *ms = open_memstream(&list, &list_len);
    CHECK(ms != NULL, "open_memstream non disponibile");
    if (ms) {
        print_supported_types(ms);
        fclose(ms);
        for (size_t i = 0; i < n; i++) {
            if (!matrix[i].type[0]) continue;
            CHECK(list && strstr(list, matrix[i].type) != NULL,
                  "l'elenco dei tipi supportati non contiene '%s'", matrix[i].type);
        }
        CHECK(list && strstr(list, "colophon") != NULL, "l'elenco dei tipi supportati non cita l'alias colophon");
        CHECK(list && strstr(list, "parties") == NULL, "l'elenco dei tipi supportati non deve contenere tipi ignoti");
        free(list);
    }

    printf("toc-policy: %s (%zu tipi dichiarati + alias colophon; %d fallimenti)\n",
           failures ? "FAIL" : "OK", n - 1, failures);
    return failures ? 1 : 0;
}

static int check_roundtrip_and_guards(void) {
    /* 1. md_line_needs_escape: prefissi che il loader reinterpreta */
    static const char *esc_yes[] = {
        "# T", "## T", "#### x", "![a](b)", "> quote", ">quote",
        "- item", "* item", "+ item", "1. item", "12) item", "```code", "| a | b |"
    };
    for (size_t i = 0; i < sizeof esc_yes / sizeof esc_yes[0]; i++)
        CHECK(md_line_needs_escape(esc_yes[i]) == 1, "riga strutturale non riconosciuta: '%s'", esc_yes[i]);
    static const char *esc_no[] = {
        "#hashtag", "*corsivo*", "!!", "1.x", "ciao", ""
    };
    for (size_t i = 0; i < sizeof esc_no / sizeof esc_no[0]; i++)
        CHECK(md_line_needs_escape(esc_no[i]) == 0, "riga di testo marcata per errore: '%s'", esc_no[i]);
    CHECK(md_line_needs_escape(NULL) == 0, "riga NULL non deve richiedere escape");

    /* 2. is_safe_chapter_file: nomi con '..' interno ma senza separatori */
    CHECK(is_safe_chapter_file("Part..1.md") == 1, "'Part..1.md' deve essere accettato");
    CHECK(is_safe_chapter_file("a/../x.md") == 0, "'a/../x.md' deve essere rifiutato");
    CHECK(is_safe_chapter_file("..") == 0, "'..' deve essere rifiutato");
    CHECK(is_safe_chapter_file(".x.md") == 0, "'.x.md' deve essere rifiutato");
    CHECK(is_safe_chapter_file("sub/x.md") == 0, "'sub/x.md' deve essere rifiutato");
    CHECK(is_safe_chapter_file("x.txt") == 0, "'x.txt' deve essere rifiutato");

    /* 3. front matter: chiusura canonica e chiusura con trattini extra */
    {
        char canon[] = "---\nid: X\n---\n\ntesto";
        const char *e = find_frontmatter_close(canon);
        CHECK(e != NULL, "chiusura '---' non riconosciuta");
        if (e) {
            CHECK(e[1] == '-' && e[2] == '-' && e[3] == '-' && e[4] == '\n',
                  "chiusura '---' identificata in posizione errata");
            char *body = (char *)e + 4; /* come il vecchio body = e + 4 */
            while (*body == '\n' || *body == '\r') body++;
            CHECK(strcmp(body, "testo") == 0, "body errato con chiusura canonica: '%s'", body);
        }
    }
    {
        char four[] = "---\nid: X\n----\n\ntesto";
        const char *e = find_frontmatter_close(four);
        CHECK(e != NULL, "chiusura '----' non riconosciuta");
        if (e) {
            const char *bnl = strchr(e + 1, '\n');
            const char *body = bnl ? bnl + 1 : e + 1 + strlen(e + 1);
            CHECK(body[0] != '-', "con chiusura '----' il body non deve iniziare con '-'");
            while (*body == '\n' || *body == '\r') body++;
            CHECK(strcmp(body, "testo") == 0, "body errato con chiusura '----': '%s'", body);
        }
    }
    CHECK(find_frontmatter_close("---\nid: X\n") == NULL, "front matter senza chiusura deve dare NULL");
    CHECK(find_frontmatter_close("---\nid: X\n--\n") == NULL, "chiusura con meno di 3 trattini non valida");

    printf("roundtrip-guards: %s (%d fallimenti)\n", failures ? "FAIL" : "OK", failures);
    return failures ? 1 : 0;
}

/* 4. round-trip reale writer -> loader: una riga che il writer marca con '\'
 * deve tornare testo puro dopo load_vault, senza essere ri-parsata come
 * struttura (blockquote, liste, tabella, codice, titoli). */
static int check_loader_unescape(void) {
    static const struct { SegType type; const char *name; } forbidden[] = {
        { SEG_BLOCKQUOTE, "SEG_BLOCKQUOTE" },
        { SEG_UL_ITEM,    "SEG_UL_ITEM" },
        { SEG_OL_ITEM,    "SEG_OL_ITEM" },
        { SEG_IMAGE,      "SEG_IMAGE" },
        { SEG_SUBHEAD,    "SEG_SUBHEAD" },
        { SEG_H3,         "SEG_H3" },
        { SEG_H4,         "SEG_H4" },
        { SEG_CODE_BLOCK, "SEG_CODE_BLOCK" },
        { SEG_TABLE,      "SEG_TABLE" },
    };
    /* testo atteso nei SEG_TEXT: le righe originali SENZA backslash */
    static const char *attese[] = {
        "Riga normale.",
        "> Non e una citazione",
        "- Non una lista",
        "1. Non una voce numerata",
        "| Non una tabella",
        "Testo dopo.",
    };

    char *tmp = mk_tmpdir();
    char yml[PATH_MAX], md[PATH_MAX];
    snprintf(yml, sizeof yml, "%s/book.yaml", tmp);
    snprintf(md, sizeof md, "%s/01-cap.md", tmp);

    FILE *f = fopen(yml, "w");
    CHECK(f != NULL, "impossibile creare %s", yml);
    if (!f) { rm_rf_path(tmp); free(tmp); return failures ? 1 : 0; }
    fprintf(f,
            "title: Test\n"
            "author: Autore\n"
            "language: it\n"
            "chapters:\n"
            "- file: 01-cap.md\n"
            "  id: cap1\n"
            "  title: Capitolo\n");
    fclose(f);

    f = fopen(md, "w");
    CHECK(f != NULL, "impossibile creare %s", md);
    if (!f) { rm_rf_path(tmp); free(tmp); return failures ? 1 : 0; }
    fprintf(f,
            "---\n"
            "id: cap1\n"
            "title: Capitolo\n"
            "---\n"
            "\n"
            "Riga normale.\n"
            "\n"
            "\\> Non e una citazione\n"
            "\n"
            "\\- Non una lista\n"
            "\n"
            "\\1. Non una voce numerata\n"
            "\n"
            "\\| Non una tabella\n"
            "\n"
            "Testo dopo.\n");
    fclose(f);

    Book bk;
    int rc = load_vault(tmp, &bk);
    CHECK(rc == 0, "load_vault ha fallito sul vault di prova (rc=%d)", rc);
    if (rc == 0) {
        CHECK(bk.nch == 1, "atteso 1 capitolo, trovati %zu", bk.nch);
        if (bk.nch >= 1) {
            Chapter *c = &bk.chs[0];
            for (size_t s = 0; s < c->nseg; s++) {
                for (size_t k = 0; k < sizeof forbidden / sizeof forbidden[0]; k++)
                    CHECK(c->segs[s].type != forbidden[k].type,
                          "riga escapata interpretata come %s (segmento %zu)",
                          forbidden[k].name, s);
            }
            /* concatena i SEG_TEXT per ritrovare le righe originali */
            char *testo = NULL;
            size_t tlen = 0;
            FILE *ms = open_memstream(&testo, &tlen);
            CHECK(ms != NULL, "open_memstream non disponibile");
            if (ms) {
                for (size_t s = 0; s < c->nseg; s++) {
                    if (c->segs[s].type != SEG_TEXT || !c->segs[s].text) continue;
                    if (tlen) fputc('\n', ms);
                    fputs(c->segs[s].text, ms);
                }
                fclose(ms);
                for (size_t k = 0; k < sizeof attese / sizeof attese[0]; k++)
                    CHECK(testo && strstr(testo, attese[k]) != NULL,
                          "il testo ricaricato non contiene '%s'", attese[k]);
                /* nessun backslash residuo: l'escape e' stato annullato */
                CHECK(testo && strchr(testo, '\\') == NULL,
                      "backslash residuo nel testo ricaricato: '%s'",
                      testo ? testo : "(null)");
                free(testo);
            }
        }
    }

    rm_rf_path(tmp);
    free(tmp);
    printf("loader-unescape: %s (%d fallimenti)\n", failures ? "FAIL" : "OK", failures);
    return failures ? 1 : 0;
}

int main(int argc, char **argv) {
    int rc = check_toc_policy();
    if (check_roundtrip_and_guards() != 0) rc = 1;
    if (check_loader_unescape() != 0) rc = 1;
    if (argc < 2) return rc;
    printf("pages=%d\n", count_pdf_pages(argv[1]));
    printf("status42=%d status1=%d\n", run_cmd_status("exit 42"), run_cmd_status("exit 1"));
    return rc;
}
