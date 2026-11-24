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

int main(int argc, char **argv) {
    int rc = check_toc_policy();
    if (argc < 2) return rc;
    printf("pages=%d\n", count_pdf_pages(argv[1]));
    printf("status42=%d status1=%d\n", run_cmd_status("exit 42"), run_cmd_status("exit 1"));
    return rc;
}
