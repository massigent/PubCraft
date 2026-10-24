/* pubcraft — Markdown Book Formatter & Publisher (C11, solo libc).
 * Compilatore da vault Markdown a PDF pronto per la stampa Amazon KDP e EPUB Kindle.
 * Supporta anche l'importazione di archivi di progetto (decompressione ZIP).
 *
 * Licenza MIT — 2026 PubCraft Contributors.
 */
#define _DARWIN_C_SOURCE 1
#define _XOPEN_SOURCE 700
#define _POSIX_C_SOURCE 200809L
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <stdint.h>
#include <ctype.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <time.h>
#ifdef _WIN32
  #include <direct.h>
  #include <io.h>
  #define mkdir(p, m) _mkdir(p)
  #ifndef PATH_MAX
    #define PATH_MAX 4096
  #endif
#else
  #include <sys/wait.h>
#endif
#include <dirent.h>
#include <unistd.h>
#include <limits.h>
#include <errno.h>
#ifdef __APPLE__
#include <mach-o/dyld.h>
#undef SEG_TEXT /* Mach-O macro conflicts with our segment enum below. */
#elif defined(_WIN32)
#include <windows.h>
#endif

#define VERSION "1.0.0-alpha.1"

static void die(const char *msg) { perror(msg); exit(1); }

static char executable_dir[PATH_MAX];
static void locate_executable(void) {
    char path[PATH_MAX] = {0};
#ifdef __APPLE__
    uint32_t n = sizeof path;
    if (_NSGetExecutablePath(path, &n) != 0) return;
    char resolved[PATH_MAX];
    if (realpath(path, resolved)) snprintf(path, sizeof path, "%s", resolved);
#elif defined(_WIN32)
    DWORD n = GetModuleFileNameA(NULL, path, sizeof path);
    if (!n || n >= sizeof path) return;
#else
    ssize_t n = readlink("/proc/self/exe", path, sizeof path - 1);
    if (n < 0) return;
    path[n] = 0;
#endif
    char *end = strrchr(path, '/');
#ifdef _WIN32
    char *back = strrchr(path, '\\');
    if (back && (!end || back > end)) end = back;
#endif
    if (end) { *end = 0; snprintf(executable_dir, sizeof executable_dir, "%s", path); }
}

/* ---------- shell quoting (per system()) ---------- */
static char *sh_quote(const char *s) {
#ifdef _WIN32
    size_t n = 3;
    for (const char *p = s; *p; p++) n += (*p == '"') ? 2 : 1;
    char *o = malloc(n + 1), *q = o;
    *q++ = '"';
    for (const char *p = s; *p; p++) {
        if (*p == '"') { *q++ = '\\'; *q++ = '"'; }
        else *q++ = *p;
    }
    *q++ = '"'; *q = 0;
    return o;
#else
    size_t n = 3;
    for (const char *p = s; *p; p++) n += (*p == '\'') ? 4 : 1;
    char *o = malloc(n), *q = o;
    *q++ = '\'';
    for (const char *p = s; *p; p++) {
        if (*p == '\'') { memcpy(q, "'\\''", 4); q += 4; }
        else *q++ = *p;
    }
    *q++ = '\''; *q = 0;
    return o;
#endif
}

/* ---------- file helpers ---------- */
static unsigned char *read_file(const char *path, size_t *out_len) {
    FILE *f = fopen(path, "rb");
    if (!f) return NULL;
    fseek(f, 0, SEEK_END);
    long n = ftell(f);
    fseek(f, 0, SEEK_SET);
    if (n < 0) { fclose(f); return NULL; }
    unsigned char *b = malloc((size_t)n + 1);
    if (!b) { fclose(f); return NULL; }
    size_t r = fread(b, 1, (size_t)n, f);
    fclose(f);
    b[r] = 0;
    if (out_len) *out_len = r;
    return b;
}
static int write_file(const char *path, const void *data, size_t n) {
    FILE *f = fopen(path, "wb");
    if (!f) return -1;
    size_t w = fwrite(data, 1, n, f);
    fclose(f);
    return w == n ? 0 : -1;
}
static void mkdir_p(const char *path) {
    char tmp[PATH_MAX];
    snprintf(tmp, sizeof tmp, "%s", path);
    for (char *p = tmp + 1; *p; p++) {
        if (*p == '/' || *p == '\\') { char c = *p; *p = 0; mkdir(tmp, 0755); *p = c; }
    }
    mkdir(tmp, 0755);
}

/* ---------- esecuzione comandi sicura con wait status ---------- */
static int run_cmd_status(const char *cmd) {
    int status = system(cmd);
    if (status == -1) return 1;
#ifndef _WIN32
    if (WIFEXITED(status)) {
        return WEXITSTATUS(status);
    }
    return 1;
#else
    return status;
#endif
}

/* ---------- confronto identita' file e copia protetta ---------- */
static int is_same_file(const char *path1, const char *path2) {
    if (!path1 || !path2) return 0;
    char r1[PATH_MAX], r2[PATH_MAX];
#ifdef _WIN32
    if (_fullpath(r1, path1, sizeof r1) && _fullpath(r2, path2, sizeof r2)) {
        if (strcmp(r1, r2) == 0) return 1;
    }
#else
    if (realpath(path1, r1) && realpath(path2, r2)) {
        if (strcmp(r1, r2) == 0) return 1;
    }
#endif
    struct stat st1, st2;
    if (stat(path1, &st1) == 0 && stat(path2, &st2) == 0) {
        if (st1.st_dev == st2.st_dev && st1.st_ino == st2.st_ino) return 1;
    }
    return 0;
}

static int safe_copy_file(const char *src_file, const char *dst_file) {
    if (is_same_file(src_file, dst_file)) return 0;
    FILE *a = fopen(src_file, "rb");
    if (!a) return -1;
    char tmp[PATH_MAX];
    snprintf(tmp, sizeof tmp, "%s.tmp%d", dst_file, (int)getpid());
    FILE *o = fopen(tmp, "wb");
    if (!o) { fclose(a); return -1; }
    char buf[65536]; size_t r;
    int ok = 1;
    while ((r = fread(buf, 1, sizeof buf, a))) {
        if (fwrite(buf, 1, r, o) != r) { ok = 0; break; }
    }
    fclose(a);
    fclose(o);
    if (ok) {
        if (rename(tmp, dst_file) != 0) {
            unlink(tmp);
            return -1;
        }
        return 0;
    } else {
        unlink(tmp);
        return -1;
    }
}

static const void *bin_search(const void *haystack, size_t haystack_len, const void *needle, size_t needle_len) {
    if (!haystack || !needle || needle_len == 0 || haystack_len < needle_len) return NULL;
    const unsigned char *h = (const unsigned char *)haystack;
    const unsigned char *n = (const unsigned char *)needle;
    unsigned char first = n[0];
    size_t max = haystack_len - needle_len;
    for (size_t i = 0; i <= max; i++) {
        if (h[i] == first && memcmp(h + i, n, needle_len) == 0) {
            return h + i;
        }
    }
    return NULL;
}

/* ---------- conteggio pagine PDF reale ---------- */
static int count_pdf_pages(const char *pdf_path) {
    size_t len = 0;
    unsigned char *data = read_file(pdf_path, &len);
    if (!data || len == 0) return 0;

    // 1. Risolvi da Catalog: cerca /Type /Catalog (o /Catalog) e il riferimento /Pages N 0 R
    const unsigned char *cat = bin_search(data, len, "/Catalog", 8);
    if (cat) {
        size_t win_start = (cat > data + 1024) ? (size_t)(cat - data - 1024) : 0;
        size_t win_end = (cat + 1024 < data + len) ? (size_t)(cat - data + 1024) : len;
        const unsigned char *pages_ref = bin_search(data + win_start, win_end - win_start, "/Pages", 6);
        if (pages_ref) {
            pages_ref += 6;
            while (pages_ref < data + len && (*pages_ref == ' ' || *pages_ref == '\t' || *pages_ref == '\r' || *pages_ref == '\n')) pages_ref++;
            char *endp = NULL;
            long obj_id = strtol((const char *)pages_ref, &endp, 10);
            if (obj_id > 0 && endp != (const char *)pages_ref) {
                char tag1[64], tag2[64], tag3[64];
                snprintf(tag1, sizeof tag1, "\n%ld 0 obj", obj_id);
                snprintf(tag2, sizeof tag2, "\r%ld 0 obj", obj_id);
                snprintf(tag3, sizeof tag3, " %ld 0 obj", obj_id);
                const unsigned char *obj = bin_search(data, len, tag1, strlen(tag1));
                if (!obj) obj = bin_search(data, len, tag2, strlen(tag2));
                if (!obj) obj = bin_search(data, len, tag3, strlen(tag3));
                if (obj) {
                    size_t obj_win = (obj + 4096 < data + len) ? 4096 : (size_t)(data + len - obj);
                    const unsigned char *cnt_str = bin_search(obj, obj_win, "/Count", 6);
                    if (cnt_str) {
                        cnt_str += 6;
                        while (cnt_str < data + len && (*cnt_str == ' ' || *cnt_str == '\t' || *cnt_str == '\r' || *cnt_str == '\n')) cnt_str++;
                        char *cendp = NULL;
                        long total = strtol((const char *)cnt_str, &cendp, 10);
                        if (total > 0 && cendp != (const char *)cnt_str) {
                            free(data);
                            return (int)total;
                        }
                    }
                }
            }
        }
    }

    // 2. Fallback: conta occorrenze di /Type /Page (foglie reali)
    int count = 0;
    for (size_t i = 0; i + 10 < len; i++) {
        if (data[i] == '/' && data[i+1] == 'T' && data[i+2] == 'y' && data[i+3] == 'p' && data[i+4] == 'e') {
            size_t j = i + 5;
            while (j < len && (data[j] == ' ' || data[j] == '\t' || data[j] == '\r' || data[j] == '\n')) j++;
            if (j + 5 <= len && memcmp(data + j, "/Page", 5) == 0) {
                if (j + 5 >= len || (data[j+5] != 's' && data[j+5] != 'S' && data[j+5] != 'b' && data[j+5] != 'l' && data[j+5] != 'L')) {
                    count++;
                }
            }
        }
    }
    free(data);
    return count;
}

/* ---------- plist XML ---------- */
static char *plist_string_last(const char *xml, const char *key) {
    char pat[256];
    snprintf(pat, sizeof pat, "<key>%s</key>", key);
    const char *k = NULL, *f = xml;
    while ((f = strstr(f, pat)) != NULL) { k = f; f += strlen(pat); }
    if (!k) return NULL;
    const char *s = strstr(k, "<string>");
    if (!s) return NULL;
    s += 8;
    const char *e = strstr(s, "</string>");
    if (!e) return NULL;
    size_t n = (size_t)(e - s);
    char *o = malloc(n + 1);
    memcpy(o, s, n); o[n] = 0;
    return o;
}
static char *plist_integer(const char *xml, const char *key) {
    char pat[256];
    snprintf(pat, sizeof pat, "<key>%s</key>", key);
    const char *k = strstr(xml, pat);
    if (!k) return NULL;
    const char *s = strstr(k, "<integer>");
    if (!s) return NULL;
    s += 9;
    const char *e = strstr(s, "</integer>");
    if (!e) return NULL;
    size_t n = (size_t)(e - s);
    char *o = malloc(n + 1);
    memcpy(o, s, n); o[n] = 0;
    return o;
}
/* Mappa id-elemento -> titolo da elementData */
typedef struct { char **ids, **titles; size_t n, cap; } IdMap;
static void idmap_add(IdMap *m, const char *id, size_t il, const char *t, size_t tl) {
    if (m->n == m->cap) {
        m->cap = m->cap ? m->cap * 2 : 32;
        m->ids = realloc(m->ids, m->cap * sizeof *m->ids);
        m->titles = realloc(m->titles, m->cap * sizeof *m->titles);
    }
    char *a = malloc(il + 1), *b = malloc(tl + 1);
    memcpy(a, id, il); a[il] = 0;
    memcpy(b, t, tl); b[tl] = 0;
    m->ids[m->n] = a; m->titles[m->n] = b; m->n++;
}
static const char *idmap_get(IdMap *m, const char *id) {
    for (size_t i = 0; i < m->n; i++)
        if (!strcmp(m->ids[i], id)) return m->titles[i];
    return NULL;
}
static int is_id_like(const char *s, size_t L) {
    const char *p = s;
    size_t l = L;
    if (l > 2 && p[0] == 'i' && p[1] == 'd') { p += 2; l -= 2; }
    if (l != 36) return 0;
    for (size_t i = 0; i < 36; i++) {
        if (i == 8 || i == 13 || i == 18 || i == 23) {
            if (p[i] != '-') return 0;
        } else if (!isxdigit((unsigned char)p[i])) return 0;
    }
    return 1;
}
static void parse_element_data(const char *xml, IdMap *m) {
    const char *ed = strstr(xml, "<key>elementData</key>");
    if (!ed) return;
    const char *p = ed;
    while ((p = strstr(p, "<key>id")) != NULL) {
        const char *ke = strstr(p, "</key>");
        if (!ke) break;
        size_t il = (size_t)(ke - (p + 5));
        if (il >= 4 && is_id_like(p + 5, il)) {
            const char *next_id = strstr(ke + 6, "<key>id");
            const char *tk = strstr(ke, "<key>title</key>");
            if (tk && (!next_id || tk < next_id)) {
                const char *s = strstr(tk, "<string>");
                if (s) {
                    s += 8;
                    const char *e = strstr(s, "</string>");
                    if (e && (!next_id || e < next_id))
                        idmap_add(m, p + 5, il, s, (size_t)(e - s));
                }
            }
        }
        p = ke + 6;
    }
}

/* ---------- parser bplist: grafo di oggetti ---------- */
static uint64_t rd_be(const unsigned char *p, int nb) {
    uint64_t v = 0;
    for (int i = 0; i < nb; i++) v = (v << 8) | p[i];
    return v;
}
typedef enum { N_NULL, N_BOOL, N_INT, N_REAL, N_DATE, N_DATA, N_STR, N_UID, N_ARRAY, N_DICT } NType;
typedef struct {
    NType type;
    char *str;               /* N_STR (UTF-8) */
    uint64_t ival;           /* N_INT/N_BOOL/N_UID */
    uint64_t *items;         /* N_ARRAY / N_DICT keys */
    uint64_t *vals;          /* N_DICT values */
    uint64_t count;
} Node;

static size_t utf16be_to_utf8(const unsigned char *s, uint64_t units, char *out) {
    size_t o = 0;
    uint64_t i = 0;
    while (i < units) {
        uint32_t u = ((uint32_t)s[2*i] << 8) | s[2*i+1];
        i++;
        if (u >= 0xD800 && u <= 0xDBFF && i < units) {
            uint32_t lo = ((uint32_t)s[2*i] << 8) | s[2*i+1];
            if (lo >= 0xDC00 && lo <= 0xDFFF) {
                u = 0x10000 + ((u - 0xD800) << 10) + (lo - 0xDC00);
                i++;
            }
        }
        if (u < 0x80) out[o++] = (char)u;
        else if (u < 0x800) {
            out[o++] = (char)(0xC0 | (u >> 6));
            out[o++] = (char)(0x80 | (u & 0x3F));
        } else if (u < 0x10000) {
            out[o++] = (char)(0xE0 | (u >> 12));
            out[o++] = (char)(0x80 | ((u >> 6) & 0x3F));
            out[o++] = (char)(0x80 | (u & 0x3F));
        } else {
            out[o++] = (char)(0xF0 | (u >> 18));
            out[o++] = (char)(0x80 | ((u >> 12) & 0x3F));
            out[o++] = (char)(0x80 | ((u >> 6) & 0x3F));
            out[o++] = (char)(0x80 | (u & 0x3F));
        }
    }
    out[o] = 0;
    return o;
}

typedef struct {
    const unsigned char *b;
    size_t n;
    Node *nodes;
    uint64_t num;
    int refSize;
    size_t *order;   /* indici ordinati per offset */
    uint64_t top;    /* indice oggetto top */
    uint64_t *objmap;  /* UID NSKeyedArchiver -> indice tabella oggetti */
    uint64_t mapcount;
} BP;

static int bp_len(BP *bp, size_t *pos, uint64_t *out) {
    if (*pos >= bp->n) return -1;
    uint64_t L = bp->b[*pos] & 0xF;
    (*pos)++;
    if (L == 0xF) {
        if (*pos >= bp->n || (bp->b[*pos] >> 4) != 0x1) return -1;
        int nb = 1 << (bp->b[*pos] & 0xF);
        (*pos)++;
        if (nb > 8 || *pos + (size_t)nb > bp->n) return -1;
        L = rd_be(bp->b + *pos, nb);
        *pos += (size_t)nb;
    }
    *out = L;
    return 0;
}
static uint64_t bp_ref(BP *bp, size_t *pos) {
    uint64_t v = 0;
    if (*pos + (size_t)bp->refSize > bp->n) return UINT64_MAX;
    v = rd_be(bp->b + *pos, bp->refSize);
    *pos += (size_t)bp->refSize;
    return v;
}

static const uint64_t *g_offs;
static size_t g_bp_num;
static int cmp_off(const void *a, const void *b_) {
    uint64_t oa = g_offs[*(const size_t *)a], ob = g_offs[*(const size_t *)b_];
    (void)g_bp_num;
    return (oa > ob) - (oa < ob);
}

static int bp_parse(BP *bp, const unsigned char *b, size_t n) {
    memset(bp, 0, sizeof *bp);
    if (n < 42 || memcmp(b, "bplist00", 8) != 0) return -1;
    const unsigned char *t = b + n - 32;
    int offSize = t[6];
    bp->refSize = t[7];
    if (offSize < 1 || offSize > 8 || bp->refSize < 1 || bp->refSize > 8) return -1;
    uint64_t num = rd_be(t + 8, 8);
    uint64_t offTab = rd_be(t + 24, 8);
    if (num == 0 || num > 2000000) return -1;
    if (offTab + num * (uint64_t)offSize > n) return -1;
    bp->b = b; bp->n = n; bp->num = num;
    uint64_t *offs = malloc(num * sizeof *offs);
    if (!offs) return -1;
    for (uint64_t i = 0; i < num; i++)
        offs[i] = rd_be(b + offTab + i * (uint64_t)offSize, offSize);
    bp->nodes = calloc(num, sizeof(Node));
    bp->order = malloc(num * sizeof(size_t));
    if (!bp->nodes || !bp->order) { free(offs); return -1; }
    for (uint64_t i = 0; i < num; i++) bp->order[i] = (size_t)i;
    g_offs = offs; g_bp_num = (size_t)num;
    qsort(bp->order, (size_t)num, sizeof(size_t), cmp_off);
    g_offs = NULL;
    for (uint64_t i = 0; i < num; i++) {
        uint64_t o = offs[i];
        Node *nd = &bp->nodes[i];
        if (o >= n) { nd->type = N_NULL; continue; }
        unsigned char m = b[o];
        int ty = m >> 4, info = m & 0xF;
        size_t pos = (size_t)o + 1;
        switch (ty) {
        case 0x0:
            nd->type = (info == 0x9) ? N_BOOL : N_NULL;
            nd->ival = (info == 0x9);
            break;
        case 0x1: {
            int nb = 1 << info;
            if (nb > 8 || pos + (size_t)nb > n) { nd->type = N_NULL; break; }
            nd->type = N_INT;
            nd->ival = rd_be(b + pos, nb);
            break;
        }
        case 0x2: case 0x3:
            nd->type = (ty == 0x2) ? N_REAL : N_DATE;
            break;
        case 0x4: {
            uint64_t L = 0;
            size_t p2 = pos - 1;
            if (bp_len(bp, &p2, &L) != 0 || p2 + L > n) { nd->type = N_NULL; break; }
            nd->type = N_DATA;
            break;
        }
        case 0x5: {
            uint64_t L = 0;
            size_t p2 = pos - 1;
            if (bp_len(bp, &p2, &L) != 0 || p2 + (size_t)L > n || L > 10000000) {
                nd->type = N_NULL; break;
            }
            nd->type = N_STR;
            nd->str = malloc((size_t)L + 1);
            if (nd->str) { memcpy(nd->str, b + p2, (size_t)L); nd->str[L] = 0; }
            break;
        }
        case 0x6: {
            uint64_t L = 0;
            size_t p2 = pos - 1;
            if (bp_len(bp, &p2, &L) != 0 || L > (n - p2) / 2 || L > 5000000) {
                nd->type = N_NULL; break;
            }
            nd->type = N_STR;
            nd->str = malloc((size_t)(4 * L + 1));
            if (nd->str) utf16be_to_utf8(b + p2, L, nd->str);
            break;
        }
        case 0x8: {
            int nb = info + 1;
            if (pos + (size_t)nb > n) { nd->type = N_NULL; break; }
            nd->type = N_UID;
            nd->ival = rd_be(b + pos, nb);
            break;
        }
        case 0xA: {
            uint64_t L = 0;
            size_t p2 = pos - 1;
            if (bp_len(bp, &p2, &L) != 0 || L > 1000000) { nd->type = N_NULL; break; }
            nd->type = N_ARRAY;
            nd->count = L;
            nd->items = malloc((L ? L : 1) * sizeof(uint64_t));
            for (uint64_t k = 0; k < L; k++) nd->items[k] = bp_ref(bp, &p2);
            break;
        }
        case 0xC:
            nd->type = N_ARRAY; nd->count = 0; nd->items = NULL;
            break;
        case 0xD: {
            uint64_t L = 0;
            size_t p2 = pos - 1;
            if (bp_len(bp, &p2, &L) != 0 || L > 1000000) { nd->type = N_NULL; break; }
            nd->type = N_DICT;
            nd->count = L;
            nd->items = malloc((L ? L : 1) * sizeof(uint64_t));
            nd->vals = malloc((L ? L : 1) * sizeof(uint64_t));
            for (uint64_t k = 0; k < L; k++) nd->items[k] = bp_ref(bp, &p2);
            for (uint64_t k = 0; k < L; k++) nd->vals[k] = bp_ref(bp, &p2);
            break;
        }
        default:
            nd->type = N_NULL;
            break;
        }
    }
    /* mappa UID: top -> $objects */
    bp->top = rd_be(t + 16, 8);
    if (bp->top < num && bp->nodes[bp->top].type == N_DICT) {
        /* cerca chiave $objects */
        Node *td = &bp->nodes[bp->top];
        for (uint64_t k = 0; k < td->count; k++) {
            uint64_t ki = td->items[k];
            if (ki < num && bp->nodes[ki].type == N_STR && bp->nodes[ki].str &&
                !strcmp(bp->nodes[ki].str, "$objects")) {
                uint64_t ai = td->vals[k];
                if (ai < num && bp->nodes[ai].type == N_ARRAY) {
                    Node *a = &bp->nodes[ai];
                    bp->mapcount = a->count;
                    bp->objmap = malloc((a->count ? a->count : 1) * sizeof(uint64_t));
                    for (uint64_t q = 0; q < a->count; q++)
                        bp->objmap[q] = a->items[q];
                }
                break;
            }
        }
    }
    free(offs); // il parse è concluso: l'array degli offset non serve più
    return 0;
}
static void bp_free(BP *bp) {
    if (!bp->nodes) return;
    for (uint64_t i = 0; i < bp->num; i++) {
        free(bp->nodes[i].str);
        free(bp->nodes[i].items);
        free(bp->nodes[i].vals);
    }
    free(bp->nodes);
    free(bp->order);
    free(bp->objmap);
    memset(bp, 0, sizeof *bp);
}
/* valore del campo `key` in un dict (indice oggetto), -1 se assente */
static int64_t dict_field(BP *bp, uint64_t di, const char *key) {
    if (di >= bp->num) return -1;
    Node *d = &bp->nodes[di];
    if (d->type != N_DICT) return -1;
    for (uint64_t k = 0; k < d->count; k++) {
        uint64_t ki = d->items[k];
        if (ki < bp->num && bp->nodes[ki].type == N_STR && bp->nodes[ki].str &&
            !strcmp(bp->nodes[ki].str, key)) {
            uint64_t vi = d->vals[k];
            return (vi < bp->num) ? (int64_t)vi : -1;
        }
    }
    return -1;
}
static const char *node_str(BP *bp, uint64_t i) {
    if (i >= bp->num || bp->nodes[i].type != N_STR) return NULL;
    return bp->nodes[i].str;
}
/* UID -> indice tabella. Gli UID NSKeyedArchiver indicizzano l'array
 * $objects, i cui elementi sono i veri indici nella tabella oggetti. */
static int64_t resolve_uid(BP *bp, uint64_t i) {
    if (i >= bp->num || bp->nodes[i].type != N_UID) return -1;
    uint64_t v = bp->nodes[i].ival;
    if (v >= bp->mapcount) return -1;
    uint64_t t = bp->objmap[v];
    return (t < bp->num) ? (int64_t)t : -1;
}
/* indice oggetto del campo `key` (risolve UID oppure diretto), -1 se assente */
static int64_t field_node(BP *bp, uint64_t di, const char *key) {
    int64_t v = dict_field(bp, di, key);
    if (v < 0) return -1;
    int64_t r = resolve_uid(bp, (uint64_t)v);
    return (r >= 0) ? r : v;
}
/* stringa del campo `key` (risolve UID), NULL se non e' stringa */
static const char *field_str(BP *bp, uint64_t di, const char *key) {
    int64_t v = field_node(bp, di, key);
    if (v < 0) return NULL;
    return node_str(bp, (uint64_t)v);
}
static const char *class_name(BP *bp, uint64_t di) {
    int64_t c = dict_field(bp, di, "$class");
    if (c < 0) return NULL;
    int64_t cd = resolve_uid(bp, (uint64_t)c);
    /* $class puo' essere UID oppure dict inline */
    uint64_t ci = (cd >= 0) ? (uint64_t)cd : (uint64_t)c;
    int64_t cn = dict_field(bp, ci, "$classname");
    if (cn < 0) return NULL;
    return node_str(bp, (uint64_t)cn);
}

/* ---------- modello libro ---------- */
typedef enum {
    SEG_TEXT,
    SEG_SUBHEAD,
    SEG_H3,
    SEG_H4,
    SEG_IMAGE,
    SEG_BLOCKQUOTE,
    SEG_UL_ITEM,
    SEG_OL_ITEM,
    SEG_SCENE_BREAK,
    SEG_CODE_BLOCK,
    SEG_TABLE
} SegType;
typedef struct { SegType type; char *text; char *imgkey; char *caption; int num; int level; } Seg;
typedef struct { char *file; char *id; char *title; char *type; Seg *segs; size_t nseg, capseg; } Chapter;
typedef struct {
    char *title, *author, *language, *fmtver, *pkgver, *bookuuid;
    char *style, *opening, *font, *scenebreak, *cover, *trim;
    int pages, bleed;
    char *vdir;
    Chapter *chs; size_t nch, capch;
    IdMap titles;
} Book;

/* font del corpo scelto nel vault (book.yaml font:) → nome famiglia CSS.
 * Solo nomi standard: se il font non è installato sul sistema, il browser
 * ricade su Georgia/serif (nessun danno). */
static const char *font_family_for(const char *id) {
    if (!id || !*id) return NULL;
    if (!strcmp(id, "ebgaramond")) return "EB Garamond";
    if (!strcmp(id, "librebaskerville")) return "Libre Baskerville";
    if (!strcmp(id, "lora")) return "Lora";
    if (!strcmp(id, "crimsonpro")) return "Crimson Pro";
    if (!strcmp(id, "spectral")) return "Spectral";
    return NULL;
}

static char *dupstr(const char *s) {
    if (!s) return NULL;
    size_t l = strlen(s);
    char *o = malloc(l + 1);
    memcpy(o, s, l + 1);
    return o;
}
static void chap_add_seg_full(Chapter *c, SegType t, const char *text,
                             const char *imgkey, const char *caption, int num, int level) {
    if (c->nseg == c->capseg) {
        c->capseg = c->capseg ? c->capseg * 2 : 8;
        c->segs = realloc(c->segs, c->capseg * sizeof *c->segs);
    }
    Seg *s = &c->segs[c->nseg++];
    s->type = t;
    s->text = dupstr(text);
    s->imgkey = dupstr(imgkey);
    s->caption = dupstr(caption);
    s->num = num;
    s->level = level;
}
static void chap_add_seg(Chapter *c, SegType t, const char *text,
                         const char *imgkey, const char *caption) {
    chap_add_seg_full(c, t, text, imgkey, caption, 0, 0);
}
static Chapter *book_new_chapter(Book *bk, const char *id, const char *title) {
    if (bk->nch == bk->capch) {
        bk->capch = bk->capch ? bk->capch * 2 : 16;
        bk->chs = realloc(bk->chs, bk->capch * sizeof *bk->chs);
    }
    Chapter *c = &bk->chs[bk->nch++];
    memset(c, 0, sizeof *c);
    c->id = dupstr(id);
    c->title = dupstr(title);
    return c;
}

/* Trova l'NSString dentro un NSAttributedString (dict o stringa diretta) */
static const char *attr_nsstring(BP *bp, uint64_t i) {
    if (i >= bp->num) return NULL;
    if (bp->nodes[i].type == N_STR) return bp->nodes[i].str;
    if (bp->nodes[i].type != N_DICT) return NULL;
    return field_str(bp, i, "NSString");
}
/* Testo del subhead: state{NS.objects:[..., NSAttributedString, ...]} */
static const char *subhead_text(BP *bp, uint64_t attach) {
    int64_t st = field_node(bp, attach, "state");
    if (st < 0 || bp->nodes[st].type != N_DICT) return NULL;
    int64_t a = field_node(bp, (uint64_t)st, "NS.objects");
    if (a < 0 || bp->nodes[a].type != N_ARRAY) return NULL;
    for (uint64_t k = 0; k < bp->nodes[a].count; k++) {
        int64_t r = resolve_uid(bp, bp->nodes[a].items[k]);
        uint64_t t = (r >= 0) ? (uint64_t)r : bp->nodes[a].items[k];
        const char *s = attr_nsstring(bp, t);
        if (s && s[0]) return s;
    }
    return NULL;
}
/* imageKey + caption di un attachment image */
static void image_info(BP *bp, uint64_t attach, const char **key, const char **cap) {
    *key = NULL; *cap = NULL;
    int64_t st = field_node(bp, attach, "state");
    if (st < 0 || bp->nodes[st].type != N_DICT) return;
    int64_t ak = field_node(bp, (uint64_t)st, "NS.keys");
    int64_t ao = field_node(bp, (uint64_t)st, "NS.objects");
    if (ak < 0 || ao < 0) return;
    Node *K = &bp->nodes[ak], *O = &bp->nodes[ao];
    if (K->type != N_ARRAY || O->type != N_ARRAY || K->count != O->count) return;
    for (uint64_t k = 0; k < K->count; k++) {
        int64_t kr = resolve_uid(bp, K->items[k]);
        const char *ks = node_str(bp, (kr >= 0) ? (uint64_t)kr : K->items[k]);
        if (!ks) continue;
        int64_t vr = resolve_uid(bp, O->items[k]);
        uint64_t vi = (vr >= 0) ? (uint64_t)vr : O->items[k];
        if (!strcmp(ks, "imageHandle")) {
            *key = field_str(bp, vi, "imageKey");
        } else if (!strcmp(ks, "imageDescription")) {
            *cap = node_str(bp, vi);
        }
    }
}

/* U+FFFC in UTF-8 */
#define FFFC "\xEF\xBF\xBC"
/* Divide il corpo sugli slot FFFC e intercala gli attachment in ordine */
static void build_segments(Chapter *c, const char *body,
                            Seg *atts, size_t nat) {
    size_t ai = 0;
    const char *start = body;
    for (const char *p = body; ; ) {
        const char *slot = strstr(p, FFFC);
        size_t len = slot ? (size_t)(slot - start) : strlen(start);
        /* testo tra uno slot e l'altro (puo' contenere \n) */
        if (len > 0) {
            char *t = malloc(len + 1);
            memcpy(t, start, len); t[len] = 0;
            /* trim spazi/newline ai bordi */
            while (*t && (*t == '\n' || *t == '\r' || *t == ' ' || *t == '\t')) memmove(t, t + 1, strlen(t));
            size_t L = strlen(t);
            while (L && (t[L-1] == '\n' || t[L-1] == '\r' || t[L-1] == ' ' || t[L-1] == '\t')) t[--L] = 0;
            if (t[0]) chap_add_seg(c, SEG_TEXT, t, NULL, NULL);
            free(t);
        }
        if (!slot) break;
        if (ai < nat) {
            Seg *a = &atts[ai++];
            if (a->type == SEG_SUBHEAD && a->text && a->text[0])
                chap_add_seg(c, SEG_SUBHEAD, a->text, NULL, NULL);
            else if (a->type == SEG_IMAGE)
                chap_add_seg(c, SEG_IMAGE, NULL, a->imgkey, a->caption);
        }
        p = slot + 3;
        start = p;
    }
    /* attachment eccedenti (slot mancanti): in coda */
    for (; ai < nat; ai++) {
        Seg *a = &atts[ai];
        if (a->type == SEG_SUBHEAD && a->text && a->text[0])
            chap_add_seg(c, SEG_SUBHEAD, a->text, NULL, NULL);
        else if (a->type == SEG_IMAGE)
            chap_add_seg(c, SEG_IMAGE, NULL, a->imgkey, a->caption);
    }
}

/* ---------- unzip / immagini ---------- */
static int unzip_to(const char *src_archive, const char *dest) {
    char *q1 = sh_quote(src_archive), *q2 = sh_quote(dest);
    char cmd[PATH_MAX * 2 + 64];
    snprintf(cmd, sizeof cmd, "unzip -q -o %s -d %s >/dev/null 2>&1", q1, q2);
    free(q1); free(q2);
    return system(cmd);
}
static char *pc_mkdtemp(char *tmpl) {
#ifdef _WIN32
    char *x = strstr(tmpl, "XXXXXX");
    if (!x) return NULL;
    static unsigned long counter = 0;
    for (int retry = 0; retry < 1000; retry++) {
        unsigned int r = (unsigned int)rand() ^ (unsigned int)(++counter) ^ (unsigned int)time(NULL);
        snprintf(x, 7, "%06x", r & 0xFFFFFF);
        if (_mkdir(tmpl) == 0) return tmpl;
        if (errno != EEXIST) return NULL;
    }
    return NULL;
#else
    return mkdtemp(tmpl);
#endif
}

static void pc_tmp_dir(char *buf, size_t sz, const char *prefix) {
#ifdef _WIN32
    const char *tmp = getenv("TEMP");
    if (!tmp) tmp = getenv("TMP");
    if (!tmp) tmp = ".";
    snprintf(buf, sz, "%s/%s-XXXXXX", tmp, prefix);
#else
    snprintf(buf, sz, "/tmp/%s-XXXXXX", prefix);
#endif
    if (!pc_mkdtemp(buf)) die("pc_mkdtemp");
}

static void rm_rf_path(const char *path) {
    char *q = sh_quote(path);
    char cmd[PATH_MAX + 64];
#ifdef _WIN32
    snprintf(cmd, sizeof cmd, "rd /s /q %s >nul 2>&1", q);
#else
    snprintf(cmd, sizeof cmd, "rm -rf %s", q);
#endif
    system(cmd);
    free(q);
}

static char *mk_tmpdir(void) {
    char buf[PATH_MAX];
    pc_tmp_dir(buf, sizeof buf, "pubcraft");
    return strdup(buf);
}
static void copy_images(const char *tmp, const char *outimg) {
    char src[PATH_MAX];
    snprintf(src, sizeof src, "%s/images", tmp);
    DIR *d = opendir(src);
    if (!d) return;
    mkdir_p(outimg);
    struct dirent *e;
    while ((e = readdir(d))) {
        if (e->d_name[0] == '.') continue;
        char sub[PATH_MAX], orig[PATH_MAX];
        snprintf(sub, sizeof sub, "%s/%s", src, e->d_name);
        snprintf(orig, sizeof orig, "%s/original", sub);
        DIR *d2 = opendir(orig);
        if (!d2) continue;
        struct dirent *f;
        while ((f = readdir(d2))) {
            if (f->d_name[0] == '.') continue;
            char sf[PATH_MAX], df[PATH_MAX];
            snprintf(sf, sizeof sf, "%s/%s", orig, f->d_name);
            snprintf(df, sizeof df, "%s/%s-%s", outimg, e->d_name, f->d_name);
            safe_copy_file(sf, df);
        }
        closedir(d2);
    }
    closedir(d);
}
/* trova il file copiato per un imageKey (prefisso "key-") */
static char *find_image_file(const char *imgd, const char *key) {
    if (!key) return NULL;
    DIR *d = opendir(imgd);
    if (!d) return NULL;
    size_t kl = strlen(key);
    struct dirent *e;
    while ((e = readdir(d))) {
        if (!strncmp(e->d_name, key, kl) && e->d_name[kl] == '-') {
            closedir(d);
            return dupstr(e->d_name);
        }
    }
    closedir(d);
    return NULL;
}
static const char *media_type_for(const char *name) {
    const char *dot = strrchr(name, '.');
    if (!dot) return "image/jpeg";
    if (!strcasecmp(dot, ".png")) return "image/png";
    if (!strcasecmp(dot, ".jpg") || !strcasecmp(dot, ".jpeg")) return "image/jpeg";
    if (!strcasecmp(dot, ".gif")) return "image/gif";
    if (!strcasecmp(dot, ".webp")) return "image/webp";
    if (!strcasecmp(dot, ".svg")) return "image/svg+xml";
    return "image/jpeg";
}

/* ---------- caricamento libro ---------- */
static int load_book(const char *src_archive, const char *tmp, Book *bk) {
    memset(bk, 0, sizeof *bk);
    if (unzip_to(src_archive, tmp) != 0) {
        fprintf(stderr, "errore: unzip fallito su '%s'\n", src_archive);
        return -1;
    }
    char p[PATH_MAX];
    snprintf(p, sizeof p, "%s/bookMetadata.plist", tmp);
    size_t n = 0;
    unsigned char *xml = read_file(p, &n);
    if (!xml) { fprintf(stderr, "errore: bookMetadata.plist assente\n"); return -1; }
    bk->title = dupstr(plist_string_last((char *)xml, "title"));
    bk->author = dupstr(plist_string_last((char *)xml, "author"));
    { char *f = plist_integer((char *)xml, "fileFormatVersion"); bk->fmtver = dupstr(f); free(f); }
    { char *v = plist_string_last((char *)xml, "vellumVersion"); bk->pkgver = dupstr(v); free(v); }
    { char *u = plist_string_last((char *)xml, "bookUUID"); bk->bookuuid = dupstr(u); free(u); }
    parse_element_data((char *)xml, &bk->titles);
    free(xml);

    snprintf(p, sizeof p, "%s/content.vellumcontent", tmp);
    unsigned char *cb = read_file(p, &n);
    if (!cb) { fprintf(stderr, "errore: archivio non valido (content mancante)\n"); return -1; }
    BP bp;
    if (bp_parse(&bp, cb, n) != 0) {
        fprintf(stderr, "errore: archivio non valido (formato binario corrotto)\n");
        free(cb);
        return -1;
    }
    /* camminata in ordine di file: capitoli + attachment.
     * Gli attachment seguono il loro capitolo: si accumulano in pending
     * e si fondono nel capitolo quando arriva il successivo (o alla fine). */
    Chapter *cur = NULL;
    const char *cur_body = NULL;
    Seg *pending = NULL;
    size_t npend = 0, cappend = 0;
    int chapno = 0;
    /* macro locale: finalizza capitolo corrente */
#define FLUSH() do { \
    if (cur && cur_body) build_segments(cur, cur_body, pending, npend); \
    for (size_t q = 0; q < npend; q++) { \
        free(pending[q].text); free(pending[q].imgkey); free(pending[q].caption); \
    } \
    npend = 0; \
} while (0)
    for (uint64_t k = 0; k < bp.num; k++) {
        uint64_t i = bp.order[k];
        if (bp.nodes[i].type != N_DICT) continue;
        const char *cn = class_name(&bp, i);
        if (!cn) continue;
        if (!strcmp(cn, "OGTypedTextElement")) {
            const char *tns = field_str(&bp, i, "typeName");
            int64_t tx = field_node(&bp, i, "text");
            if (tx < 0 || !tns) continue;
            const char *body = attr_nsstring(&bp, (uint64_t)tx);
            if (!body || !body[0]) continue;
            const char *us = field_str(&bp, i, "uniqueID");
            const char *title = us ? idmap_get(&bk->titles, us) : NULL;
            char fb[64];
            chapno++;
            if (!title) { snprintf(fb, sizeof fb, "Capitolo %d", chapno); title = fb; }
            FLUSH();
            cur = book_new_chapter(bk, us ? us : "", title);
            cur_body = body;
        } else if (!strcmp(cn, "OGMutableAttachmentFormat")) {
            const char *ks = field_str(&bp, i, "kind");
            if (!ks) continue;
            if (npend == cappend) {
                cappend = cappend ? cappend * 2 : 8;
                pending = realloc(pending, cappend * sizeof *pending);
            }
            Seg *s = &pending[npend++];
            memset(s, 0, sizeof *s);
            if (!strcmp(ks, "subhead")) {
                const char *t = subhead_text(&bp, i);
                s->type = SEG_SUBHEAD;
                s->text = dupstr(t ? t : "");
            } else if (!strcmp(ks, "image")) {
                const char *key = NULL, *cap = NULL;
                image_info(&bp, i, &key, &cap);
                s->type = SEG_IMAGE;
                s->imgkey = dupstr(key);
                s->caption = dupstr(cap);
            } else {
                npend--; /* attachment sconosciuto: ignora */
            }
        }
    }
    FLUSH();
#undef FLUSH
    for (size_t q = 0; q < npend; q++) {
        free(pending[q].text); free(pending[q].imgkey); free(pending[q].caption);
    }
    free(pending);
    bp_free(&bp);
    free(cb);
    return 0;
}

/* ---------- HTML e Markdown Inline ---------- */
static void fprint_esc_char(FILE *f, char c) {
    switch (c) {
        case '&': fputs("&amp;", f); break;
        case '<': fputs("&lt;", f); break;
        case '>': fputs("&gt;", f); break;
        case '"': fputs("&quot;", f); break;
        default: fputc(c, f);
    }
}

static void fprint_esc(FILE *f, const char *s) {
    if (!s) return;
    for (; *s; s++) {
        fprint_esc_char(f, *s);
    }
}

static void fprint_md_inline(FILE *f, const char *s) {
    if (!s) return;
    const char *p = s;
    while (*p) {
        // Codice inline `codice`
        if (*p == '`') {
            const char *end = strchr(p + 1, '`');
            if (end) {
                fputs("<code>", f);
                for (const char *q = p + 1; q < end; q++) fprint_esc_char(f, *q);
                fputs("</code>", f);
                p = end + 1;
                continue;
            }
        }
        // Link [testo](url)
        if (*p == '[') {
            const char *cb = strchr(p + 1, ']');
            if (cb && cb[1] == '(') {
                const char *cp = strchr(cb + 2, ')');
                if (cp) {
                    fputs("<a href=\"", f);
                    for (const char *q = cb + 2; q < cp; q++) fprint_esc_char(f, *q);
                    fputs("\">", f);
                    size_t tlen = (size_t)(cb - (p + 1));
                    char *sub = malloc(tlen + 1);
                    if (sub) {
                        memcpy(sub, p + 1, tlen);
                        sub[tlen] = 0;
                        fprint_md_inline(f, sub);
                        free(sub);
                    }
                    fputs("</a>", f);
                    p = cp + 1;
                    continue;
                }
            }
        }
        // Grassetto e corsivo *** o ___
        if ((p[0] == '*' && p[1] == '*' && p[2] == '*') || (p[0] == '_' && p[1] == '_' && p[2] == '_')) {
            char m = p[0];
            if (m == '_' && p > s && isalnum((unsigned char)p[-1])) {
                fprint_esc_char(f, *p);
                p++;
                continue;
            }
            const char *q = p + 3;
            const char *end = NULL;
            while (*q) {
                if (q[0] == m && q[1] == m && q[2] == m) {
                    if (m != '_' || !isalnum((unsigned char)q[3])) {
                        end = q; break;
                    }
                }
                q++;
            }
            if (end && end > p + 3) {
                fputs("<strong><em>", f);
                size_t n = (size_t)(end - (p + 3));
                char *sub = malloc(n + 1);
                if (sub) {
                    memcpy(sub, p + 3, n); sub[n] = 0;
                    fprint_md_inline(f, sub);
                    free(sub);
                }
                fputs("</em></strong>", f);
                p = end + 3;
                continue;
            }
        }
        // Grassetto ** o __
        if ((p[0] == '*' && p[1] == '*') || (p[0] == '_' && p[1] == '_')) {
            char m = p[0];
            if (m == '_' && p > s && isalnum((unsigned char)p[-1])) {
                fprint_esc_char(f, *p);
                p++;
                continue;
            }
            const char *q = p + 2;
            const char *end = NULL;
            while (*q) {
                if (q[0] == m && q[1] == m) {
                    if (m != '_' || !isalnum((unsigned char)q[2])) {
                        end = q; break;
                    }
                }
                q++;
            }
            if (end && end > p + 2) {
                fputs("<strong>", f);
                size_t n = (size_t)(end - (p + 2));
                char *sub = malloc(n + 1);
                if (sub) {
                    memcpy(sub, p + 2, n); sub[n] = 0;
                    fprint_md_inline(f, sub);
                    free(sub);
                }
                fputs("</strong>", f);
                p = end + 2;
                continue;
            }
        }
        // Corsivo * o _
        if (*p == '*' || *p == '_') {
            char m = *p;
            if (m == '_' && p > s && isalnum((unsigned char)p[-1])) {
                fprint_esc_char(f, *p);
                p++;
                continue;
            }
            const char *q = p + 1;
            const char *end = NULL;
            while (*q) {
                if (*q == m) {
                    if (m != '_' || !isalnum((unsigned char)q[1])) {
                        end = q; break;
                    }
                }
                q++;
            }
            if (end && end > p + 1) {
                fputs("<em>", f);
                size_t n = (size_t)(end - (p + 1));
                char *sub = malloc(n + 1);
                if (sub) {
                    memcpy(sub, p + 1, n); sub[n] = 0;
                    fprint_md_inline(f, sub);
                    free(sub);
                }
                fputs("</em>", f);
                p = end + 1;
                continue;
            }
        }
        fprint_esc_char(f, *p);
        p++;
    }
}
/* Stampa paragrafo iniziale avvolgendo il primo carattere alfabetico in <span class="dropcap"> */
static void fprint_esc_with_dropcap(FILE *f, const char *s) {
    if (!s) return;
    const char *p = s;
    while (*p == ' ' || *p == '\t') p++;
    while (*p) {
        if (*p == '"' || *p == '\'' || *p == '`') {
            if (*p == '"') fputs("&quot;", f);
            else fputc(*p, f);
            p++;
        } else if ((unsigned char)p[0] == 0xC2 && ((unsigned char)p[1] == 0xAB || (unsigned char)p[1] == 0xBB)) {
            fwrite(p, 1, 2, f);
            p += 2;
        } else if ((unsigned char)p[0] == 0xE2 && (unsigned char)p[1] == 0x80 &&
                   ((unsigned char)p[2] >= 0x98 && (unsigned char)p[2] <= 0x9F)) {
            fwrite(p, 1, 3, f);
            p += 3;
        } else {
            break;
        }
    }
    if (!*p) return;

    size_t glen = 0;
    unsigned char c = (unsigned char)*p;
    if (c < 0x80) {
        if (isalpha(c)) glen = 1;
    } else if ((c & 0xE0) == 0xC0 && (p[1] & 0xC0) == 0x80) {
        glen = 2;
    } else if ((c & 0xF0) == 0xE0 && (p[1] & 0xC0) == 0x80 && (p[2] & 0xC0) == 0x80) {
        glen = 3;
    } else if ((c & 0xF8) == 0xF0 && (p[1] & 0xC0) == 0x80 && (p[2] & 0xC0) == 0x80 && (p[3] & 0xC0) == 0x80) {
        glen = 4;
    }

    if (glen > 0) {
        const char *cls = "dropcap";
        if (glen == 1 && (*p == 'I' || *p == 'i')) {
            cls = "dropcap dropcap-i";
        } else if (glen == 1 && (*p == 'J' || *p == 'j')) {
            cls = "dropcap dropcap-j";
        } else if (glen == 2 && (unsigned char)p[0] == 0xC3 &&
                   ((unsigned char)p[1] >= 0x8C && (unsigned char)p[1] <= 0x8F)) {
            cls = "dropcap dropcap-i";
        }
        fprintf(f, "<span class=\"%s\">", cls);
        for (size_t i = 0; i < glen; i++) {
            if (p[i] == '&') fputs("&amp;", f);
            else if (p[i] == '<') fputs("&lt;", f);
            else if (p[i] == '>') fputs("&gt;", f);
            else fputc(p[i], f);
        }
        fprintf(f, "</span>");
        p += glen;
    }
    fprint_md_inline(f, p);
}

/* Stampa paragrafo iniziale avvolgendo le prime 3 parole in <span class="first-phrase"> */
static void fprint_esc_with_first_phrase(FILE *f, const char *s) {
    if (!s) return;
    const char *p = s;
    while (*p == ' ' || *p == '\t') p++;
    int words = 0;
    const char *end_phrase = p;
    while (*end_phrase && words < 3) {
        if (*end_phrase == ' ' || *end_phrase == '\t') {
            words++;
            while (*end_phrase == ' ' || *end_phrase == '\t') end_phrase++;
        } else if (*end_phrase == ',' || *end_phrase == '.' || *end_phrase == ';' || *end_phrase == ':' || *end_phrase == '!' || *end_phrase == '?') {
            end_phrase++;
            break;
        } else {
            end_phrase++;
        }
    }
    if (end_phrase > p) {
        fprintf(f, "<span class=\"first-phrase\">");
        for (const char *q = p; q < end_phrase; q++) {
            if (*q == '&') fputs("&amp;", f);
            else if (*q == '<') fputs("&lt;", f);
            else if (*q == '>') fputs("&gt;", f);
            else if (*q == '"') fputs("&quot;", f);
            else fputc(*q, f);
        }
        fprintf(f, "</span>");
        fprint_md_inline(f, end_phrase);
    } else {
        fprint_md_inline(f, p);
    }
}
/* interruzione di scena: riga fatta solo di *, - e spazi (***, * * *, ---) */
static int is_scene_break(const char *s);

/* paragrafi: dividi su \n, scarta righe vuote */
static void fprint_paras(FILE *f, const char *t) {
    const char *p = t;
    while (*p) {
        while (*p == '\n' || *p == '\r') p++;
        if (!*p) break;
        const char *e = strchr(p, '\n');
        size_t L = e ? (size_t)(e - p) : strlen(p);
        while (L && (p[L-1] == '\r' || p[L-1] == ' ' || p[L-1] == '\t')) L--;
        if (L) {
            char *s = malloc(L + 1);
            memcpy(s, p, L); s[L] = 0;
            if (is_scene_break(s)) {
                fprintf(f, "<hr class=\"sb\"/>\n");
            } else {
                fprintf(f, "<p>");
                fprint_md_inline(f, s);
                fprintf(f, "</p>\n");
            }
            free(s);
        }
        p = e ? e + 1 : p + L;
    }
}

static void fprint_paras_epub(FILE *f, const char *t, const char *opening, int *is_first, int is_ded_or_epi) {
    const char *p = t;
    while (*p) {
        while (*p == '\n' || *p == '\r') p++;
        if (!*p) break;
        const char *e = strchr(p, '\n');
        size_t L = e ? (size_t)(e - p) : strlen(p);
        while (L && (p[L-1] == '\r' || p[L-1] == ' ' || p[L-1] == '\t')) L--;
        if (L) {
            char *s = malloc(L + 1);
            memcpy(s, p, L); s[L] = 0;
            if (is_scene_break(s)) {
                fprintf(f, "<hr class=\"sb\"/>\n");
                if (is_first) *is_first = 1;
            } else if (is_ded_or_epi) {
                fprintf(f, "<p class=\"noindent\">");
                fprint_md_inline(f, s);
                fprintf(f, "</p>\n");
            } else if (is_first && *is_first) {
                fprintf(f, "<p class=\"noindent first-para\">");
                if (opening && !strcmp(opening, "smallcaps")) {
                    fprint_esc_with_first_phrase(f, s);
                } else if (opening && !strcmp(opening, "none")) {
                    fprint_md_inline(f, s);
                } else {
                    fprint_esc_with_dropcap(f, s);
                }
                fprintf(f, "</p>\n");
                *is_first = 0;
            } else {
                fprintf(f, "<p>");
                fprint_md_inline(f, s);
                fprintf(f, "</p>\n");
            }
            free(s);
        }
        p = e ? e + 1 : p + L;
    }
}

/* verifica se una riga e' un separatore/delimitatore di tabella markdown (es. | --- | :---: | ---: |) */
static int is_table_delimiter_row(const char *s) {
    if (!s || !strchr(s, '|')) return 0;
    while (*s == ' ' || *s == '\t') s++;
    if (*s == '|') s++;
    int num_cols = 0;
    while (*s) {
        while (*s == ' ' || *s == '\t') s++;
        if (!*s) break;
        if (*s == '|') { s++; continue; }
        const char *cell_start = s;
        while (*s && *s != '|') s++;
        const char *cell_end = s;
        while (cell_end > cell_start && (cell_end[-1] == ' ' || cell_end[-1] == '\t')) cell_end--;
        if (cell_end <= cell_start) continue;
        int has_hyphen = 0;
        for (const char *p = cell_start; p < cell_end; p++) {
            if (*p == '-') has_hyphen = 1;
            else if (*p != ':' && *p != ' ' && *p != '\t') return 0;
        }
        if (!has_hyphen) return 0;
        num_cols++;
    }
    return num_cols > 0;
}

static int is_table_start(const char *t, const char *next_raw) {
    if (!t || !strchr(t, '|') || !next_raw) return 0;
    const char *next_nl = strchr(next_raw, '\n');
    size_t nlen = next_nl ? (size_t)(next_nl - next_raw) : strlen(next_raw);
    char *peek = malloc(nlen + 1);
    if (!peek) return 0;
    memcpy(peek, next_raw, nlen);
    peek[nlen] = 0;
    while (nlen && (peek[nlen - 1] == '\r' || peek[nlen - 1] == ' ' || peek[nlen - 1] == '\t')) peek[--nlen] = 0;
    char *pt = peek;
    while (*pt == '\r' || *pt == ' ' || *pt == '\t') pt++;
    int res = is_table_delimiter_row(pt);
    free(peek);
    return res;
}

/* estrae le celle di una riga di tabella delimitata da pipe */
static void parse_table_cells(const char *line, char ***cells_out, size_t *ncells_out) {
    *cells_out = NULL;
    *ncells_out = 0;
    if (!line) return;
    const char *p = line;
    while (*p == ' ' || *p == '\t') p++;
    if (*p == '|') p++;
    char **cells = NULL;
    size_t count = 0, cap = 0;
    while (*p) {
        const char *cell_start = p;
        while (*p && *p != '|') p++;
        const char *cell_end = p;
        while (cell_end > cell_start && (cell_end[-1] == ' ' || cell_end[-1] == '\t' || cell_end[-1] == '\r')) cell_end--;
        while (cell_start < cell_end && (*cell_start == ' ' || *cell_start == '\t')) cell_start++;

        if (*p == 0 && cell_end <= cell_start) break;

        size_t len = (cell_end > cell_start) ? (size_t)(cell_end - cell_start) : 0;
        char *cell = malloc(len + 1);
        if (cell) {
            if (len > 0) memcpy(cell, cell_start, len);
            cell[len] = 0;
            if (count == cap) {
                cap = cap ? cap * 2 : 4;
                cells = realloc(cells, cap * sizeof(char *));
            }
            cells[count++] = cell;
        }
        if (*p == '|') p++;
    }
    *cells_out = cells;
    *ncells_out = count;
}

/* stampa una tabella markdown in formato HTML tipografico pulito */
static void fprint_table_html(FILE *f, const char *raw) {
    if (!f || !raw || !*raw) return;
    char *copy = dupstr(raw);
    if (!copy) return;

    char **lines = NULL;
    size_t nlines = 0, line_cap = 0;
    char *cur = copy;
    while (*cur) {
        char *nl = strchr(cur, '\n');
        if (nl) *nl = 0;
        char *cr = strchr(cur, '\r');
        if (cr) *cr = 0;
        if (cur[0]) {
            if (nlines == line_cap) {
                line_cap = line_cap ? line_cap * 2 : 8;
                lines = realloc(lines, line_cap * sizeof(char *));
            }
            lines[nlines++] = cur;
        }
        if (!nl) break;
        cur = nl + 1;
    }

    if (nlines < 2) {
        fprintf(f, "<p>");
        fprint_md_inline(f, raw);
        fprintf(f, "</p>\n");
        free(lines);
        free(copy);
        return;
    }

    char **headers = NULL;
    size_t ncols = 0;
    parse_table_cells(lines[0], &headers, &ncols);
    if (ncols == 0) {
        free(lines);
        free(copy);
        return;
    }

    char **delims = NULL;
    size_t ndelims = 0;
    parse_table_cells(lines[1], &delims, &ndelims);

    int *aligns = calloc(ncols, sizeof(int));
    if (aligns) {
        for (size_t i = 0; i < ncols; i++) {
            if (i < ndelims && delims[i]) {
                const char *d = delims[i];
                size_t dl = strlen(d);
                if (dl >= 2 && d[0] == ':' && d[dl - 1] == ':') aligns[i] = 1; /* center */
                else if (dl >= 1 && d[dl - 1] == ':') aligns[i] = 2; /* right */
                else aligns[i] = 0; /* left */
            }
        }
    }

    fprintf(f, "<div class=\"table-wrap\"><table class=\"book-table\">\n");
    fprintf(f, "<thead>\n<tr>\n");
    for (size_t i = 0; i < ncols; i++) {
        const char *al = (aligns && aligns[i] == 1) ? "center" : ((aligns && aligns[i] == 2) ? "right" : "left");
        fprintf(f, "  <th style=\"text-align:%s;\">", al);
        fprint_md_inline(f, headers[i]);
        fprintf(f, "</th>\n");
    }
    fprintf(f, "</tr>\n</thead>\n");

    fprintf(f, "<tbody>\n");
    for (size_t r = 2; r < nlines; r++) {
        char **row_cells = NULL;
        size_t nrow_cells = 0;
        parse_table_cells(lines[r], &row_cells, &nrow_cells);
        fprintf(f, "<tr>\n");
        for (size_t c = 0; c < ncols; c++) {
            const char *al = (aligns && aligns[c] == 1) ? "center" : ((aligns && aligns[c] == 2) ? "right" : "left");
            fprintf(f, "  <td style=\"text-align:%s;\">", al);
            if (c < nrow_cells && row_cells[c]) {
                fprint_md_inline(f, row_cells[c]);
            }
            fprintf(f, "</td>\n");
        }
        fprintf(f, "</tr>\n");
        for (size_t k = 0; k < nrow_cells; k++) free(row_cells[k]);
        free(row_cells);
    }
    fprintf(f, "</tbody>\n");
    fprintf(f, "</table></div>\n");

    for (size_t i = 0; i < ncols; i++) free(headers[i]);
    free(headers);
    for (size_t i = 0; i < ndelims; i++) free(delims[i]);
    free(delims);
    free(aligns);
    free(lines);
    free(copy);
}

static int cmd_info(const char *archive_path) {
    char *tmp = mk_tmpdir();
    Book bk;
    int rc = load_book(archive_path, tmp, &bk);
    if (rc == 0) {
        printf("titolo:   %s\n", bk.title ? bk.title : "(sconosciuto)");
        printf("autore:   %s\n", bk.author ? bk.author : "(sconosciuto)");
        printf("uuid:     %s\n", bk.bookuuid ? bk.bookuuid : "-");
        printf("formato:  %s (%s)\n", bk.fmtver ? bk.fmtver : "?",
               bk.pkgver ? bk.pkgver : "-");
        size_t nseg = 0, nimg = 0, nsub = 0, tot = 0;
        for (size_t i = 0; i < bk.nch; i++) {
            for (size_t j = 0; j < bk.chs[i].nseg; j++) {
                Seg *s = &bk.chs[i].segs[j];
                nseg++;
                if (s->type == SEG_IMAGE) nimg++;
                else if (s->type == SEG_SUBHEAD) nsub++;
                else if (s->text) tot += strlen(s->text);
            }
        }
        printf("capitoli: %zu | blocchi: %zu (testo+sottotitoli %zu, immagini %zu)\n",
               bk.nch, nseg, nseg - nimg, nimg);
        printf("sottotitoli: %zu | caratteri corpo: %zu\n", nsub, tot);
        for (size_t i = 0; i < bk.nch; i++)
            printf("  %2zu. %s\n", i + 1, bk.chs[i].title);
    }
    rm_rf_path(tmp);
    free(tmp);
    return rc;
}

static int cmd_list(const char *archive_path) {
    char *qv = sh_quote(archive_path);
    char cmd[PATH_MAX + 64];
    snprintf(cmd, sizeof cmd, "unzip -l %s", qv);
    free(qv);
    return system(cmd);
}

static const char *BOOK_CSS =
    "body{font-family:Georgia,'Times New Roman',serif;line-height:1.65;"
    "max-width:42em;margin:2em auto;padding:0 1.2em;color:#1a1a1a}\n"
    "header{border-bottom:2px solid #333;margin-bottom:2em;text-align:center}\n"
    "h1{font-size:2em;margin:.2em 0}h2{font-size:1.5em;margin-top:2em;text-align:center}\n"
    "h3{font-size:1.2em;margin-top:1.6em}\n"
    ".byline{color:#555;font-style:italic}\n"
    "p{margin:.9em 0;text-align:justify}\n"
    "hr.sb,hr{border:none;margin:2em 0;text-align:center;height:auto}\n"
    "hr.sb::after,hr::after{content:'* * *';display:block;letter-spacing:.6em;color:#1a1a1a;font-size:1em}\n"
    "hr+p,hr.sb+p{text-indent:0}\n"
    "figure{margin:1.4em auto;text-align:center}\n"
    "figcaption{font-size:.9em;color:#555;font-style:italic}\n"
    "img{max-width:100%;height:auto;display:block;margin:1em auto}\n"
    ".dedication{margin-top:20vh;text-align:center;font-style:italic}\n"
    ".dedication h2{display:none}\n"
    ".dedication p{text-align:center;text-indent:0;line-height:1.8}\n"
    ".epigraph{margin-top:18vh;font-style:italic;max-width:80%;margin-left:auto;margin-right:auto}\n"
    ".epigraph h2{display:none}\n"
    ".epigraph p{text-indent:0;text-align:justify}\n"
    ".epigraph p:last-of-type{text-align:right;font-style:normal;margin-top:1em}\n"
    ".about-author figure img{max-width:150px;border-radius:50%;margin:1em auto;display:block}\n"
    ".table-wrap{margin:1.4em auto;overflow-x:auto}\n"
    "table.book-table,table{width:100%;border-collapse:collapse;margin:0 auto;font-size:.9em}\n"
    "table.book-table th,table.book-table td,table th,table td{padding:.35em .6em;border-bottom:1px solid #ccc;vertical-align:top}\n"
    "table.book-table th,table th{font-weight:bold;border-top:1.5px solid #222;border-bottom:1.5px solid #222;font-variant:small-caps}\n"
    "table.book-table tr:last-child td,table tr:last-child td{border-bottom:1.5px solid #222}\n"
    ".table-wrap+p,table+p,table.book-table+p{text-indent:0}\n";

/* risolvi imageKey -> file copiato (solo percorsi da archivio) */
static void resolve_archive_images(Book *bk, const char *imgd) {
    for (size_t i = 0; i < bk->nch; i++)
        for (size_t j = 0; j < bk->chs[i].nseg; j++) {
            Seg *s = &bk->chs[i].segs[j];
            if (s->type == SEG_IMAGE && s->imgkey && !s->text) {
                char *fn = find_image_file(imgd, s->imgkey);
                free(s->text);
                s->text = fn; /* riuso text come nome file */
            }
        }
}
/* copia images/ di un vault (nomi invariati, protetta da sovrascritture identiche) */
static void copy_vault_images(const char *vdir, const char *outimg) {
    char src[PATH_MAX];
    snprintf(src, sizeof src, "%s/images", vdir);
    DIR *d = opendir(src);
    if (!d) return;
    mkdir_p(outimg);
    struct dirent *e;
    while ((e = readdir(d))) {
        if (e->d_name[0] == '.') continue;
        char sf[PATH_MAX], df[PATH_MAX];
        snprintf(sf, sizeof sf, "%s/%s", src, e->d_name);
        snprintf(df, sizeof df, "%s/%s", outimg, e->d_name);
        safe_copy_file(sf, df);
    }
    closedir(d);
}
/* copia fonts (dal vault, da fonts/, dalla GUI o dal sistema) dentro outdir/fonts */
static void copy_fonts_from_dir(const char *src, const char *outfonts) {
    if (!src || !*src) return;
    DIR *d = opendir(src);
    if (!d) return;
    mkdir_p(outfonts);
    struct dirent *e;
    while ((e = readdir(d))) {
        if (e->d_name[0] == '.') continue;
        const char *ext = strrchr(e->d_name, '.');
        if (!ext) continue;
        if (strcasecmp(ext, ".ttf") && strcasecmp(ext, ".otf") &&
            strcasecmp(ext, ".woff") && strcasecmp(ext, ".woff2")) continue;
        char sf[PATH_MAX], df[PATH_MAX];
        snprintf(sf, sizeof sf, "%s/%s", src, e->d_name);
        snprintf(df, sizeof df, "%s/%s", outfonts, e->d_name);
        if (access(df, F_OK) != 0) {
            safe_copy_file(sf, df);
        }
    }
    closedir(d);
}

static void copy_fonts(const char *vdir, const char *outfonts) {
    char src[PATH_MAX];
    if (vdir && *vdir) {
        snprintf(src, sizeof src, "%s/fonts", vdir);
        copy_fonts_from_dir(src, outfonts);
    }
    const char *configured = getenv("PUBCRAFT_FONTS_DIR");
    if (configured) copy_fonts_from_dir(configured, outfonts);
    if (executable_dir[0]) {
        snprintf(src, sizeof src, "%s/fonts", executable_dir);
        copy_fonts_from_dir(src, outfonts);
        snprintf(src, sizeof src, "%s/../Resources/fonts", executable_dir);
        copy_fonts_from_dir(src, outfonts);
        snprintf(src, sizeof src, "%s/../share/pubcraft/fonts", executable_dir);
        copy_fonts_from_dir(src, outfonts);
    }
    copy_fonts_from_dir("fonts", outfonts);
    copy_fonts_from_dir("src/pubcraftgui/web/static/fonts", outfonts);
    copy_fonts_from_dir("/usr/local/share/pubcraft/fonts", outfonts);
    copy_fonts_from_dir("/usr/share/pubcraft/fonts", outfonts);
}

static void emit_font_faces(FILE *f, const char *fonts_dir) {
    if (!fonts_dir) return;
    DIR *d = opendir(fonts_dir);
    if (!d) return;
    struct dirent *e;
    while ((e = readdir(d))) {
        if (e->d_name[0] == '.') continue;
        const char *ext = strrchr(e->d_name, '.');
        if (!ext) continue;
        const char *fmt = NULL;
        if (!strcasecmp(ext, ".woff2")) fmt = "woff2";
        else if (!strcasecmp(ext, ".woff")) fmt = "woff";
        else if (!strcasecmp(ext, ".ttf")) fmt = "truetype";
        else if (!strcasecmp(ext, ".otf")) fmt = "opentype";
        if (!fmt) continue;

        const char *name = e->d_name;
        const char *family = NULL;
        int weight = 400;
        const char *style = "normal";

        if (strstr(name, "700") || strstr(name, "Bold") || strstr(name, "bold")) weight = 700;
        else if (strstr(name, "600") || strstr(name, "SemiBold")) weight = 600;
        else if (strstr(name, "500") || strstr(name, "Medium")) weight = 500;

        if (strstr(name, "i400") || strstr(name, "Italic") || strstr(name, "italic")) style = "italic";

        if (!strncmp(name, "ebgaramond", 10)) family = "EB Garamond";
        else if (!strncmp(name, "librebaskerville", 16)) family = "Libre Baskerville";
        else if (!strncmp(name, "lora", 4)) family = "Lora";
        else if (!strncmp(name, "crimsonpro", 10)) family = "Crimson Pro";
        else if (!strncmp(name, "spectral", 8)) family = "Spectral";
        else if (!strncmp(name, "GreatVibes", 10)) family = "Great Vibes";
        else if (!strncmp(name, "CormorantSC", 11)) family = "Cormorant SC";
        else if (!strncmp(name, "Cormorant", 9)) family = "Cormorant";
        else if (!strncmp(name, "CinzelDecorative", 16)) family = "Cinzel Decorative";
        else if (!strncmp(name, "BodoniModa", 10)) family = "Bodoni Moda";
        else if (!strncmp(name, "MrsSaintDelafield", 17)) family = "Mrs Saint Delafield";
        else if (!strncmp(name, "Montserrat", 10)) family = "Montserrat";

        if (family) {
            fprintf(f, "@font-face {\n  font-family: '%s';\n  src: url('fonts/%s') format('%s');\n  font-weight: %d;\n  font-style: %s;\n}\n",
                    family, name, fmt, weight, style);
        }
    }
    closedir(d);
}

static void emit_custom_style(FILE *f, const char *vdir) {
    if (!vdir) return;
    char path[PATH_MAX];
    snprintf(path, sizeof path, "%s/style.css", vdir);
    FILE *sf = fopen(path, "r");
    if (sf) {
        char buf[4096];
        size_t n;
        while ((n = fread(buf, 1, sizeof buf, sf)) > 0) {
            fwrite(buf, 1, n, f);
        }
        fclose(sf);
        fprintf(f, "\n");
    }
}

static void emit_preset_style(FILE *f, const char *style_id) {
    if (!style_id || !*style_id || !strcmp(style_id, "classico") || !strcmp(style_id, "classic")) {
        fprintf(f,
            ".chapter { color: #1a1815; line-height: 1.62; }\n"
            ".chapter h2 { font-family: 'Palatino', 'Palatino Linotype', Georgia, serif; font-size: 1.35em; font-weight: normal; text-align: center; text-transform: uppercase; letter-spacing: .15em; margin: 1.8em auto .3em; line-height: 1.45; }\n"
            ".chapter .dropcap { font-family: 'Great Vibes', 'Apple Chancery', cursive, Georgia, serif; font-size: 3.9em; font-weight: normal; font-style: normal; float: left; line-height: .66; margin-top: .04em; margin-bottom: -.06em; margin-right: .13em; }\n"
        );
    } else if (!strcmp(style_id, "oxford") || !strcmp(style_id, "editoriale")) {
        fprintf(f,
            ".chapter { color: #1c1917; line-height: 1.68; }\n"
            ".chapter h2 { font-family: 'Cormorant SC', 'Cormorant', Garamond, serif; font-size: 1.48em; font-weight: 600; text-align: center; text-transform: uppercase; letter-spacing: .18em; margin: 1.9em auto .3em; line-height: 1.4; }\n"
            ".chapter .dropcap { font-family: 'Cormorant', Garamond, serif; font-size: 3.8em; font-weight: 600; font-style: normal; float: left; line-height: .74; margin-top: .06em; margin-bottom: -.04em; margin-right: .12em; }\n"
        );
    } else if (!strcmp(style_id, "meridian")) {
        fprintf(f,
            ".chapter { color: #171614; line-height: 1.65; }\n"
            ".chapter h2 { font-family: 'Baskerville', 'Times New Roman', serif; font-size: 1.4em; font-weight: normal; text-align: center; text-transform: uppercase; letter-spacing: .18em; margin: 2em auto .3em; }\n"
            ".chapter .dropcap { font-family: 'Cinzel Decorative', 'Cinzel', 'Baskerville', serif; font-size: 3.7em; font-weight: normal; font-style: normal; float: left; line-height: .72; margin-top: .06em; margin-bottom: -.04em; margin-right: .14em; }\n"
        );
    } else if (!strcmp(style_id, "whisper") || !strcmp(style_id, "romantico")) {
        fprintf(f,
            ".chapter { color: #211e1a; line-height: 1.74; }\n"
            ".chapter h2 { font-family: Georgia, serif; font-size: 1.35em; font-weight: normal; text-align: center; letter-spacing: .12em; text-transform: uppercase; margin: 2.2em auto .3em; }\n"
        );
    }
}

typedef struct {
    int depth;
    SegType types[16];
    int item_open[16];
} ListState;

static void list_close_to(FILE *f, ListState *ls, int target_depth) {
    while (ls->depth > target_depth) {
        int idx = ls->depth - 1;
        if (ls->item_open[idx]) {
            fprintf(f, "</li>\n");
            ls->item_open[idx] = 0;
        }
        if (ls->types[idx] == SEG_UL_ITEM) {
            fprintf(f, "</ul>\n");
        } else {
            fprintf(f, "</ol>\n");
        }
        ls->depth--;
    }
}

static void list_close_all(FILE *f, ListState *ls) {
    list_close_to(f, ls, 0);
}

static void list_handle_item(FILE *f, ListState *ls, SegType type, int level, int num, const char *text) {
    if (level < 0) level = 0;
    if (level > 8) level = 8;

    if (level < ls->depth && ls->types[level] != type) {
        list_close_to(f, ls, level);
    } else if (level < ls->depth) {
        list_close_to(f, ls, level + 1);
    }

    while (ls->depth <= level) {
        int idx = ls->depth;
        ls->types[idx] = type;
        ls->item_open[idx] = 0;
        if (type == SEG_UL_ITEM) {
            fprintf(f, "<ul>\n");
        } else {
            if (num > 1 && idx == level) fprintf(f, "<ol start=\"%d\">\n", num);
            else fprintf(f, "<ol>\n");
        }
        ls->depth++;
    }

    int cur_idx = level;
    if (ls->item_open[cur_idx]) {
        fprintf(f, "</li>\n");
        ls->item_open[cur_idx] = 0;
    }

    fprintf(f, "<li>");
    fprint_md_inline(f, text ? text : "");
    ls->item_open[cur_idx] = 1;
}

static int export_html_book(Book *bk, const char *imgd, const char *outdir) {
    (void)imgd;
    mkdir_p(outdir);
    char hp[PATH_MAX];
    snprintf(hp, sizeof hp, "%s/index.html", outdir);
    FILE *f = fopen(hp, "w");
    if (!f) die("fopen index.html");
    fprintf(f, "<!DOCTYPE html>\n<html lang=\"it\">\n<head>\n<meta charset=\"utf-8\">\n"
               "<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">\n"
               "<title>");
    fprint_esc(f, bk->title ? bk->title : "Libro");
    fprintf(f, "</title>\n<style>\n%s\n</style>\n</head>\n<body>\n<header>\n<h1>", BOOK_CSS);
    fprint_esc(f, bk->title ? bk->title : "Senza titolo");
    fprintf(f, "</h1>\n<p class=\"byline\">");
    fprint_esc(f, bk->author ? bk->author : "");
    fprintf(f, "</p>\n<p class=\"byline\">Generato da PubCraft %s</p>\n</header>\n<main>\n", VERSION);
    for (size_t i = 0; i < bk->nch; i++) {
        fprintf(f, "<section>\n<h2>");
        fprint_esc(f, bk->chs[i].title);
        fprintf(f, "</h2>\n");
        ListState ls = {0};
        int in_bq = 0;
        for (size_t j = 0; j < bk->chs[i].nseg; j++) {
            Seg *s = &bk->chs[i].segs[j];
            if (s->type != SEG_UL_ITEM && s->type != SEG_OL_ITEM) { list_close_all(f, &ls); }
            if (s->type != SEG_BLOCKQUOTE && in_bq) { fprintf(f, "</blockquote>\n"); in_bq = 0; }

            if (s->type == SEG_SUBHEAD) {
                fprintf(f, "<h3>");
                fprint_md_inline(f, s->text ? s->text : "");
                fprintf(f, "</h3>\n");
            } else if (s->type == SEG_H3) {
                fprintf(f, "<h4>");
                fprint_md_inline(f, s->text ? s->text : "");
                fprintf(f, "</h4>\n");
            } else if (s->type == SEG_H4) {
                fprintf(f, "<h5>");
                fprint_md_inline(f, s->text ? s->text : "");
                fprintf(f, "</h5>\n");
            } else if (s->type == SEG_BLOCKQUOTE) {
                if (!in_bq) {
                    fprintf(f, "<blockquote>\n");
                    in_bq = 1;
                }
                fprintf(f, "<p>");
                fprint_md_inline(f, s->text ? s->text : "");
                fprintf(f, "</p>\n");
            } else if (s->type == SEG_UL_ITEM) {
                list_handle_item(f, &ls, SEG_UL_ITEM, s->level, s->num, s->text);
            } else if (s->type == SEG_OL_ITEM) {
                list_handle_item(f, &ls, SEG_OL_ITEM, s->level, s->num, s->text);
            } else if (s->type == SEG_CODE_BLOCK) {
                fprintf(f, "<pre><code>");
                fprint_esc(f, s->text ? s->text : "");
                fprintf(f, "</code></pre>\n");
            } else if (s->type == SEG_SCENE_BREAK) {
                fprintf(f, "<hr class=\"sb\"/>\n");
            } else if (s->type == SEG_TABLE) {
                fprint_table_html(f, s->text);
            } else if (s->type == SEG_IMAGE) {
                if (s->text) {
                    fprintf(f, "<figure><img src=\"images/");
                    fprint_esc(f, s->text);
                    fprintf(f, "\" alt=\"");
                    fprint_esc(f, s->caption ? s->caption : "");
                    fprintf(f, "\">");
                    if (s->caption && s->caption[0]) {
                        fprintf(f, "<figcaption>");
                        fprint_md_inline(f, s->caption);
                        fprintf(f, "</figcaption>");
                    }
                    fprintf(f, "</figure>\n");
                }
            } else if (s->text) {
                fprint_paras(f, s->text);
            }
        }
        list_close_all(f, &ls);
        if (in_bq) fprintf(f, "</blockquote>\n");
        fprintf(f, "</section>\n");
    }
    fprintf(f, "</main>\n</body>\n</html>\n");
    fclose(f);
    printf("HTML scritto in %s/index.html (%zu capitoli)\n", outdir, bk->nch);
    return 0;
}

static int export_html(const char *archive_path, const char *outdir) {
    char *tmp = mk_tmpdir();
    Book bk;
    int rc = load_book(archive_path, tmp, &bk);
    if (rc == 0) {
        char imgd[PATH_MAX];
        snprintf(imgd, sizeof imgd, "%s/images", outdir);
        copy_images(tmp, imgd);
        resolve_archive_images(&bk, imgd);
        rc = export_html_book(&bk, imgd, outdir);
    }
    rm_rf_path(tmp);
    free(tmp);
    return rc;
}

/* kindle=1: prima immagine marcata come copertina + checklist KDP */
static int export_epub_book(Book *bk, const char *imgd, const char *outepub, int kindle) {
    char abs_outepub[PATH_MAX];
    if (outepub[0] == '/' || (outepub[0] && outepub[1] == ':')) {
        snprintf(abs_outepub, sizeof abs_outepub, "%s", outepub);
    } else {
        char cwd[PATH_MAX];
        if (getcwd(cwd, sizeof cwd)) snprintf(abs_outepub, sizeof abs_outepub, "%s/%s", cwd, outepub);
        else snprintf(abs_outepub, sizeof abs_outepub, "%s", outepub);
    }
    const char *lang = (bk->language && bk->language[0]) ? bk->language : "it";

    char buildt[PATH_MAX];
    pc_tmp_dir(buildt, sizeof buildt, "pubcraft-epub");
    char oebps[PATH_MAX], metainf[PATH_MAX];
    snprintf(oebps, sizeof oebps, "%s/OEBPS", buildt);
    snprintf(metainf, sizeof metainf, "%s/META-INF", buildt);
    mkdir_p(oebps); mkdir_p(metainf);
    /* immagini dentro OEBPS/images (imgd e' gia' popolato dal chiamante) */
    {
        char dst[PATH_MAX];
        snprintf(dst, sizeof dst, "%s/images", oebps);
        mkdir_p(dst);
        DIR *d = opendir(imgd);
        if (d) {
            struct dirent *e;
            while ((e = readdir(d))) {
                if (e->d_name[0] == '.') continue;
                char sf[PATH_MAX], df[PATH_MAX];
                snprintf(sf, sizeof sf, "%s/%s", imgd, e->d_name);
                snprintf(df, sizeof df, "%s/%s", dst, e->d_name);
                safe_copy_file(sf, df);
            }
            closedir(d);
        }
    }
    char p[PATH_MAX];
    snprintf(p, sizeof p, "%s/mimetype", buildt);
    write_file(p, "application/epub+zip", 20);
    snprintf(p, sizeof p, "%s/container.xml", metainf);
    const char *container_xml =
        "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n"
        "<container version=\"1.0\" xmlns=\"urn:oasis:names:tc:opendocument:xmlns:container\">\n"
        "  <rootfiles><rootfile full-path=\"OEBPS/content.opf\" "
        "media-type=\"application/oebps-package+xml\"/></rootfiles>\n</container>\n";
    write_file(p, container_xml, strlen(container_xml));

    snprintf(p, sizeof p, "%s/style.css", oebps);
    {
        FILE *fcss = fopen(p, "w");
        if (fcss) {
            fprintf(fcss, "%s\n", BOOK_CSS);
            const char *ff = font_family_for(bk->font);
            if (ff) {
                fprintf(fcss, "body { font-family: '%s', Georgia, serif; }\n", ff);
            }
            emit_preset_style(fcss, bk->style);

            fprintf(fcss, ".chapter .first-para { text-indent: 0; }\n");
            fprintf(fcss, ".chapter .dropcap { font-size: 3.2em; float: left; line-height: .75; margin-right: .12em; margin-top: .04em; }\n");
            fprintf(fcss, ".chapter .smallcaps { font-variant: small-caps; letter-spacing: .08em; font-weight: 600; }\n");

            if (bk->scenebreak && (!strcmp(bk->scenebreak, "fleuron") || !strcmp(bk->scenebreak, "ornament"))) {
                fprintf(fcss, "hr.sb::after, hr::after { content: '❦'; font-size: 1.3em; letter-spacing: normal; }\n");
            } else if (bk->scenebreak && !strcmp(bk->scenebreak, "line")) {
                fprintf(fcss, "hr.sb, hr { border-top: 1px solid #777; width: 35%%; height: 1px; margin: 2em auto; }\n");
                fprintf(fcss, "hr.sb::after, hr::after { content: none; }\n");
            } else if (bk->scenebreak && !strcmp(bk->scenebreak, "dinkus")) {
                fprintf(fcss, "hr.sb::after, hr::after { content: '• • •'; letter-spacing: .8em; }\n");
            } else if (bk->scenebreak && !strcmp(bk->scenebreak, "space")) {
                fprintf(fcss, "hr.sb, hr { height: 1.5em; border: none; margin: 0; }\n");
                fprintf(fcss, "hr.sb::after, hr::after { content: none; }\n");
            }
            fclose(fcss);
        } else {
            write_file(p, BOOK_CSS, strlen(BOOK_CSS));
        }
    }
    for (size_t c = 0; c < bk->nch; c++) {
        int is_ded = bk->chs[c].type && !strcmp(bk->chs[c].type, "dedication");
        int is_epi = bk->chs[c].type && !strcmp(bk->chs[c].type, "epigraph");
        int is_auth = bk->chs[c].type && !strcmp(bk->chs[c].type, "about-author");
        int is_ack = bk->chs[c].type && !strcmp(bk->chs[c].type, "acknowledgments");
        const char *cls = "chapter";
        if (is_ded) cls = "dedication";
        else if (is_epi) cls = "epigraph";
        else if (is_auth) cls = "chapter about-author";
        else if (is_ack) cls = "chapter acknowledgments";

        char cp[PATH_MAX];
        snprintf(cp, sizeof cp, "%s/chap%03zu.xhtml", oebps, c + 1);
        FILE *f = fopen(cp, "w");
        fprintf(f, "<?xml version=\"1.0\" encoding=\"utf-8\"?>\n"
                   "<!DOCTYPE html>\n<html xmlns=\"http://www.w3.org/1999/xhtml\" lang=\"%s\">\n"
                   "<head><title>", lang);
        fprint_esc(f, bk->chs[c].title);
        fprintf(f, "</title><link rel=\"stylesheet\" href=\"style.css\"/></head>\n<body class=\"%s\">\n", cls);
        if (!is_ded && !is_epi) {
            fprintf(f, "<h2>");
            fprint_esc(f, bk->chs[c].title);
            fprintf(f, "</h2>\n");
        }
        ListState ls = {0};
        int in_bq = 0;
        int is_first_p = 1;
        for (size_t j = 0; j < bk->chs[c].nseg; j++) {
            Seg *s = &bk->chs[c].segs[j];
            if (s->type != SEG_UL_ITEM && s->type != SEG_OL_ITEM) { list_close_all(f, &ls); }
            if (s->type != SEG_BLOCKQUOTE && in_bq) { fprintf(f, "</blockquote>\n"); in_bq = 0; }

            if (s->type == SEG_SUBHEAD) {
                fprintf(f, "<h3>");
                fprint_md_inline(f, s->text ? s->text : "");
                fprintf(f, "</h3>\n");
            } else if (s->type == SEG_H3) {
                fprintf(f, "<h4>");
                fprint_md_inline(f, s->text ? s->text : "");
                fprintf(f, "</h4>\n");
            } else if (s->type == SEG_H4) {
                fprintf(f, "<h5>");
                fprint_md_inline(f, s->text ? s->text : "");
                fprintf(f, "</h5>\n");
            } else if (s->type == SEG_BLOCKQUOTE) {
                if (!in_bq) {
                    fprintf(f, "<blockquote>\n");
                    in_bq = 1;
                }
                fprintf(f, "<p>");
                fprint_md_inline(f, s->text ? s->text : "");
                fprintf(f, "</p>\n");
            } else if (s->type == SEG_UL_ITEM) {
                list_handle_item(f, &ls, SEG_UL_ITEM, s->level, s->num, s->text);
            } else if (s->type == SEG_OL_ITEM) {
                list_handle_item(f, &ls, SEG_OL_ITEM, s->level, s->num, s->text);
            } else if (s->type == SEG_CODE_BLOCK) {
                fprintf(f, "<pre><code>");
                fprint_esc(f, s->text ? s->text : "");
                fprintf(f, "</code></pre>\n");
            } else if (s->type == SEG_SCENE_BREAK) {
                fprintf(f, "<hr class=\"sb\"/>\n");
                is_first_p = 1;
            } else if (s->type == SEG_TABLE) {
                fprint_table_html(f, s->text);
                is_first_p = 1;
            } else if (s->type == SEG_IMAGE) {
                if (s->text) {
                    fprintf(f, "<figure><img src=\"images/");
                    fprint_esc(f, s->text);
                    fprintf(f, "\" alt=\"");
                    fprint_esc(f, s->caption ? s->caption : "");
                    fprintf(f, "\"/>");
                    if (s->caption && s->caption[0]) {
                        fprintf(f, "<figcaption>");
                        fprint_md_inline(f, s->caption);
                        fprintf(f, "</figcaption>");
                    }
                    fprintf(f, "</figure>\n");
                }
            } else if (s->text) {
                fprint_paras_epub(f, s->text, bk->opening, &is_first_p, is_ded || is_epi);
            }
        }
        list_close_all(f, &ls);
        if (in_bq) fprintf(f, "</blockquote>\n");
        fprintf(f, "</body>\n</html>\n");
        fclose(f);
    }

    /* nav.xhtml obbligatorio per EPUB 3 */
    char navp[PATH_MAX];
    snprintf(navp, sizeof navp, "%s/nav.xhtml", oebps);
    FILE *fn = fopen(navp, "w");
    if (fn) {
        fprintf(fn, "<?xml version=\"1.0\" encoding=\"utf-8\"?>\n"
                    "<!DOCTYPE html>\n"
                    "<html xmlns=\"http://www.w3.org/1999/xhtml\" xmlns:epub=\"http://www.idpf.org/2007/ops\" lang=\"%s\">\n"
                    "<head><title>Indice</title><link rel=\"stylesheet\" href=\"style.css\"/></head>\n"
                    "<body>\n"
                    "  <nav epub:type=\"toc\" id=\"toc\">\n"
                    "    <h2>Indice</h2>\n"
                    "    <ol>\n", lang);
        for (size_t c = 0; c < bk->nch; c++) {
            fprintf(fn, "      <li><a href=\"chap%03zu.xhtml\">", c + 1);
            fprint_esc(fn, bk->chs[c].title ? bk->chs[c].title : "Capitolo");
            fprintf(fn, "</a></li>\n");
        }
        fprintf(fn, "    </ol>\n  </nav>\n</body>\n</html>\n");
        fclose(fn);
    }

    /* immagini usate (per il manifest) */
    char **used = NULL;
    size_t nused = 0, capused = 0;
    for (size_t c = 0; c < bk->nch; c++)
        for (size_t j = 0; j < bk->chs[c].nseg; j++) {
            Seg *s = &bk->chs[c].segs[j];
            if (s->type == SEG_IMAGE && s->text) {
                size_t k;
                for (k = 0; k < nused; k++)
                    if (!strcmp(used[k], s->text)) break;
                if (k == nused) {
                    if (nused == capused) {
                        capused = capused ? capused * 2 : 16;
                        used = realloc(used, capused * sizeof *used);
                    }
                    used[nused++] = s->text;
                }
            }
        }

    const char *cover_file = NULL;
    char exp_cover_buf[PATH_MAX];
    if (bk->cover && bk->cover[0]) {
        const char *bn = strrchr(bk->cover, '/');
        bn = bn ? bn + 1 : bk->cover;
        snprintf(exp_cover_buf, sizeof exp_cover_buf, "%s/images/%s", oebps, bn);
        if (access(exp_cover_buf, F_OK) == 0) {
            cover_file = bn;
        } else if (bk->vdir) {
            char src_cov[PATH_MAX];
            snprintf(src_cov, sizeof src_cov, "%s/%s", bk->vdir, bk->cover);
            if (access(src_cov, F_OK) == 0) {
                safe_copy_file(src_cov, exp_cover_buf);
                cover_file = bn;
            }
        }
    }

    snprintf(p, sizeof p, "%s/content.opf", oebps);
    FILE *opf = fopen(p, "w");
    fprintf(opf, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n"
                 "<package version=\"3.0\" unique-identifier=\"uid\" "
                 "xmlns=\"http://www.idpf.org/2007/opf\">\n<metadata "
                 "xmlns:dc=\"http://purl.org/dc/elements/1.1/\">\n"
                 "<dc:identifier id=\"uid\">urn:uuid:%s</dc:identifier>\n",
            bk->bookuuid ? bk->bookuuid : "pubcraft-book");
    fprintf(opf, "<dc:title>");
    fprint_esc(opf, bk->title ? bk->title : "Senza titolo");
    fprintf(opf, "</dc:title>\n<dc:creator>");
    fprint_esc(opf, bk->author ? bk->author : "");
    fprintf(opf, "</dc:creator>\n<dc:language>%s</dc:language>\n", lang);
    if (cover_file)
        fprintf(opf, "<meta name=\"cover\" content=\"cover\"/>\n");
    fprintf(opf, "<meta property=\"dcterms:modified\">2026-10-20T00:00:00Z</meta>\n"
                 "</metadata>\n<manifest>\n");
    fprintf(opf, "<item id=\"nav\" href=\"nav.xhtml\" media-type=\"application/xhtml+xml\" properties=\"nav\"/>\n");
    for (size_t c = 0; c < bk->nch; c++)
        fprintf(opf, "<item id=\"c%zu\" href=\"chap%03zu.xhtml\" media-type=\"application/xhtml+xml\"/>\n",
                c + 1, c + 1);
    if (cover_file) {
        fprintf(opf, "<item id=\"cover\" href=\"images/%s\" media-type=\"%s\" properties=\"cover-image\"/>\n",
                cover_file, media_type_for(cover_file));
    }
    for (size_t k = 0; k < nused; k++) {
        if (cover_file && !strcmp(used[k], cover_file)) continue;
        fprintf(opf, "<item id=\"img%zu\" href=\"images/%s\" media-type=\"%s\"/>\n",
                k, used[k], media_type_for(used[k]));
    }
    fprintf(opf, "<item id=\"css\" href=\"style.css\" media-type=\"text/css\"/>\n</manifest>\n<spine>\n");
    for (size_t c = 0; c < bk->nch; c++) fprintf(opf, "<itemref idref=\"c%zu\"/>\n", c + 1);
    fprintf(opf, "</spine>\n</package>\n");
    fclose(opf);
    free(used);

    char *qb = sh_quote(buildt), *qo = sh_quote(abs_outepub);
    char cmd[PATH_MAX * 3];
    snprintf(cmd, sizeof cmd,
             "cd %s && rm -f %s && zip -X0 -q %s mimetype >/dev/null && "
             "zip -rDX9 -q %s . -x mimetype >/dev/null", qb, qo, qo, qo);
    free(qb); free(qo);
    int rc = run_cmd_status(cmd);
    struct stat st;
    if (rc == 0 && stat(abs_outepub, &st) == 0 && st.st_size > 0) {
        printf("EPUB scritto in %s (%zu capitoli)\n", abs_outepub, bk->nch);
        if (kindle) {
            printf("\nChecklist pubblicazione Kindle (KDP):\n");
            if (cover_file) {
                printf("  [1] Copertina: '%s' marcata come cover-image (KDP: JPEG/TIFF, lato lungo 2560px).\n", cover_file);
            } else {
                printf("  [1] Copertina: nessuna copertina specificata nel libro. (Le illustrazioni interne NON sono state forzate come copertina).\n");
            }
            printf("  [2] Carica questo EPUB direttamente su kdp.amazon.com (il MOBI e' deprecato).\n");
            printf("  [3] Anteprima con Kindle Previewer (gratuito, Amazon) prima di pubblicare.\n");
        }
    } else {
        fprintf(stderr, "errore: generazione EPUB fallita o file non valido (rc=%d)\n", rc);
        rc = 1;
    }
    /* la tmpdir di build si può ormai rimuovere */
    rm_rf_path(buildt);
    return rc;
}

static int export_epub_impl(const char *archive_path, const char *outepub, int kindle) {
    char *tmp = mk_tmpdir();
    Book bk;
    int rc = load_book(archive_path, tmp, &bk);
    if (rc == 0) {
        char imgd[PATH_MAX];
        snprintf(imgd, sizeof imgd, "%s/pubcraft-img-XXXXXX", tmp);
        if (!pc_mkdtemp(imgd)) die("pc_mkdtemp");
        copy_images(tmp, imgd);
        resolve_archive_images(&bk, imgd);
        rc = export_epub_book(&bk, imgd, outepub, kindle);
    }
    rm_rf_path(tmp);
    free(tmp);
    return rc;
}

static int cmd_open(const char *archive_path) {
    char outdir[PATH_MAX];
    pc_tmp_dir(outdir, sizeof outdir, "pubcraft-open");
    if (export_html(archive_path, outdir) != 0) return 1;
    char *q = sh_quote(outdir);
    char cmd[PATH_MAX + 128];
#if defined(__APPLE__)
    snprintf(cmd, sizeof cmd, "open %s/index.html", q);
#elif defined(_WIN32)
    snprintf(cmd, sizeof cmd, "start \"\" %s\\index.html", q);
#else
    snprintf(cmd, sizeof cmd, "xdg-open %s/index.html >/dev/null 2>&1 || sensible-browser %s/index.html >/dev/null 2>&1", q, q);
#endif
    free(q);
    return system(cmd);
}

/* ---------- stampa: HTML impaginato con misure KDP ---------- */
struct Trim { const char *name; double w, h; };
static const struct Trim TRIMS[] = {
    {"5x8", 5, 8}, {"5.25x8", 5.25, 8}, {"5.5x8.5", 5.5, 8.5},
    {"6x9", 6, 9}, {"7x10", 7, 10}, {"8x10", 8, 10},
    {"8.5x8.5", 8.5, 8.5}, {"8.5x11", 8.5, 11}, {NULL, 0, 0}
};
static double kdp_gutter(int pages) {
    if (pages <= 150) return 0.375;
    if (pages <= 300) return 0.5;
    if (pages <= 500) return 0.625;
    if (pages <= 700) return 0.75;
    if (pages <= 800) return 0.875;
    return 1.0;
}

#define PUBCRAFT_SWASH_SVG "data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 288 96'%3E%3Cpath d='M243.442 20.752c0 3.5-2.226 6.372-6.668 8.628-4.454 2.25-11.172 3.524-20.172 3.828-.21 3-1.258 6.046-3.148 9.148 3.61 1.106 6.75 3 9.446 5.704 2.704 2.698 4.054 5.944 4.054 9.75 0 3.398-1.172 6.55-3.528 9.446s-5.39 5.39-9.14 7.5c-3.75 2.102-7.972 3.726-12.668 4.87-4.7 1.142-9.398 1.724-14.11 1.724-3.398 0-6.808-.278-10.2-.84-3.396-.548-7.146-1.462-11.25-2.766-4.114-1.3-8.778-3.058-14.028-5.25-5.25-2.2-11.528-4.946-18.828-8.25a296.124 296.124 0 01-11.102 7.722c-3.7 2.45-7.348 4.582-10.95 6.376-3.602 1.794-7.148 3.222-10.648 4.28-3.5 1.048-6.902 1.58-10.204 1.58-4.996 0-9.07-1.228-12.226-3.68-3.148-2.446-4.976-5.63-5.472-9.516-1.102.202-2.652.422-4.648.664-2 .262-3.95.376-5.852.376-3.3 0-6.352-.432-9.148-1.288-2.796-.856-5.25-2.106-7.352-3.75a16.764 16.764 0 01-4.872-6.08c-1.152-2.382-1.726-5.132-1.726-8.25 0-4.296 1.296-8.026 3.898-11.172 2.602-3.148 6-5.746 10.2-7.796 4.2-2.046 8.95-3.578 14.25-4.578 5.3-1 10.652-1.5 16.05-1.5.796-2.598 2.328-4.922 4.578-6.976 2.25-2.046 4.95-3.796 8.102-5.25 3.148-1.45 6.68-2.546 10.578-3.296a62.583 62.583 0 0111.852-1.128c3.496 0 7.32.324 11.476.98 4.144.648 8.32 1.45 12.524 2.398 4.196.954 8.266 1.98 12.226 3.078a1624.86 1624.86 0 0110.578 3c4.996-2.5 9.696-4.454 14.102-5.852 4.398-1.394 9.304-2.098 14.708-2.098 2.484 0 5.042.356 7.636 1.05 2.594.696 4.75 1.5 6.438 2.398 2.898-1 5.958-1.894 9.152-2.7 3.2-.796 6.796-1.2 10.804-1.2 4.692 0 8.418.876 11.168 2.624 2.75 1.748 4.12 3.796 4.12 6.092zm-110.248 20.1c-6.102-2.102-11.528-3.876-16.274-5.328-4.75-1.444-8.976-2.644-12.67-3.596-3.704-.95-6.954-1.654-9.75-2.102-2.8-.454-5.25-.726-7.348-.828l-.3 2.25c-.3 2.204-.204 4 .3 5.398.496 1.406 1.176 2.5 2.02 3.3.848.796 1.754 1.376 2.704 1.73.946.352 1.77.52 2.476.52.3 0 .372.128.218.372-.148.254-.472.324-.968.23-.704 0-1.676-.2-2.93-.602-1.246-.394-2.454-1.05-3.602-1.95-1.144-.898-2.144-2.074-3-3.524-.852-1.446-1.27-3.226-1.27-5.328 0-.898.102-1.746.3-2.546-4.304 0-8.73.324-13.282.976-4.546.652-8.672 1.828-12.376 3.528-3.696 1.708-6.726 4.074-9.07 7.122-2.352 3.054-3.524 6.93-3.524 11.626 0 6 1.624 10.628 4.872 13.876 3.248 3.248 7.476 4.872 12.672 4.872 2 0 3.95-.102 5.852-.298 1.894-.202 3.394-.452 4.5-.75.094-2.902.898-5.48 2.398-7.73s3.5-4.196 6-5.852c2.504-1.648 5.296-2.894 8.402-3.75 3.102-.856 6.296-1.266 9.598-1.266 2.898 0 5.726.27 8.48.82 2.746.548 4.672.97 5.77 1.278a261.25 261.25 0 009.902-6 466.17 466.17 0 009.9-6.448zm-22.792 14.25c-1.304-.598-3.128-1.146-5.48-1.652-2.352-.49-4.624-.75-6.82-.75-5.906 0-10.8 1.156-14.7 3.456-3.902 2.3-5.852 5.652-5.852 10.046v1.204c0 .5.046.898.148 1.2 2.102-.496 4.55-1.246 7.352-2.25 2.792-.996 5.7-2.122 8.7-3.376 3-1.242 5.976-2.542 8.922-3.888 2.952-1.364 5.526-2.688 7.73-3.99zm21.448 8.552a526.336 526.336 0 01-10.726-4.426c-3.656-1.544-6.726-2.716-9.23-3.53-4.5 2.308-9.472 4.678-14.922 7.132-5.454 2.456-11.778 4.784-18.976 6.976.696 3.5 2.52 6.294 5.476 8.388 2.946 2.112 6.372 3.148 10.274 3.148 3.196 0 6.426-.508 9.672-1.5 3.25-1.004 6.496-2.34 9.75-3.974a86.157 86.157 0 009.598-5.69c3.152-2.14 6.178-4.32 9.084-6.524zm28.2-39.902c-3.208-1-6.878-2.098-11.03-3.296a442.362 442.362 0 00-12.602-3.454 193.844 193.844 0 00-12.296-2.77c-3.958-.75-7.328-1.128-10.128-1.128-7.5 0-13.554 1.2-18.152 3.602-4.602 2.398-7.402 6.102-8.398 11.098 5.296.2 12.176 1.004 20.624 2.402 8.458 1.394 18.23 4 29.332 7.796 1.2-.796 2.746-1.82 4.648-3.07a224.47 224.47 0 016.152-3.898c2.204-1.352 4.352-2.672 6.446-3.972 2.1-1.306 3.904-2.408 5.404-3.31zm-7.2 24.454c-2.396-.902-5.274-1.976-8.626-3.23a658.83 658.83 0 00-9.676-3.52 507.986 507.986 0 01-9.75 6.226c-3.3 2.054-6.554 4.07-9.75 6.074 1.2.304 2.624.726 4.27 1.278 1.648.55 3.394 1.148 5.25 1.8 1.856.648 3.726 1.328 5.628 2.024a97.95 97.95 0 015.25 2.108c3-2.3 5.93-4.476 8.778-6.53 2.848-2.054 5.726-4.134 8.626-6.23zm69.752 9.744c0-7.884-3.352-12.744-10.05-14.544-2.602 3.5-6.45 6.37-11.554 8.626-5.098 2.25-11.394 3.374-18.894 3.374-2.398 0-4.624-.146-6.676-.456a47.129 47.129 0 01-6.222-1.348 116.453 116.453 0 01-6.75-2.168 272.6 272.6 0 00-8.25-2.782 606.44 606.44 0 00-8.624 6.296 627.121 627.121 0 01-8.774 6.454c2.796 1.108 6.454 2.626 10.954 4.574 4.5 1.958 9.32 3.866 14.476 5.708a183.078 183.078 0 0015.826 4.876c5.39 1.394 10.382 2.098 15 2.098 3.5 0 6.996-.348 10.5-1.05 3.5-.696 6.676-1.868 9.528-3.524a20.518 20.518 0 006.898-6.372c1.724-2.604 2.612-5.86 2.612-9.762zm-41.704-28.494a678.603 678.603 0 00-9.528-2.48c-3.75-.95-7.074-1.872-9.972-2.77-1.886 1.2-3.95 2.476-6.14 3.82a449.982 449.982 0 00-6.376 3.976 381.345 381.345 0 01-5.78 3.602c-1.798 1.106-3.254 2.05-4.348 2.852 1.5.496 3.126 1.07 4.874 1.728a343.96 343.96 0 009.97 3.524c1.5.504 2.746.898 3.75 1.2a283.346 283.346 0 0111.626-8.024 316.173 316.173 0 0111.924-7.428zm32.098 3.75c-4.102 0-8.648-.23-13.644-.68-5.008-.45-10.554-1.324-16.648-2.628a194.624 194.624 0 00-12.216 7.282 176.687 176.687 0 00-11.626 8.172c6 2.3 11.646 4.154 16.95 5.546 5.294 1.406 9.394 2.112 12.3 2.112 5.394 0 9.972-.876 13.722-2.624 3.75-1.748 6.622-4.226 8.622-7.422-.602-.2-1.696-.3-3.3-.3-.89 0-2.032.078-3.372.23a18.393 18.393 0 00-3.828.828c-.398.204-.648.148-.75-.148-.11-.304.102-.554.602-.75.898-.398 2.11-.704 3.61-.906 1.5-.196 2.942-.296 4.356-.296.614 0 1.188.024 1.804.078.602.046 1.054.07 1.352.07a20.823 20.823 0 001.418-4.196c.34-1.5.554-2.974.648-4.368zm-8.394-15.904c-1.304-1-3.078-1.824-5.328-2.476-2.25-.652-4.422-.976-6.528-.976-4.402 0-8.602.574-12.598 1.726-4.008 1.152-8.402 2.922-13.208 5.32 1.308.406 2.932.876 4.874 1.43 1.95.55 3.894 1.106 5.864 1.648 1.946.55 3.714 1.028 5.32 1.422 1.598.406 2.696.704 3.304.902 1.594-.902 3.27-1.8 5.02-2.7a512.407 512.407 0 015.176-2.628c1.7-.856 3.254-1.606 4.652-2.25 1.392-.648 2.542-1.118 3.452-1.418zm8.25 14.404c.196-2.598-.43-5.206-1.876-7.8-1.454-2.598-3.176-4.546-5.176-5.852-1 .398-2.278.926-3.824 1.57a72.807 72.807 0 00-4.8 2.25c-1.644.856-3.286 1.708-4.878 2.554-1.618.848-3 1.624-4.2 2.32 1.5.398 3.286.876 5.336 1.43 2.04.55 4.16 1.07 6.368 1.57a97.51 97.51 0 006.676 1.27c2.24.362 4.366.582 6.374.688zm26.09-10.204c0-2.192-.996-4.07-3-5.628-1.992-1.546-5.106-2.32-9.292-2.32-3.704 0-6.86.3-9.458.902a57.075 57.075 0 00-7.792 2.398c2.296 1.5 4.124 3.704 5.472 6.602 1.336 2.894 1.918 5.7 1.714 8.398 6.8 0 12.222-.876 16.286-2.628 4.054-1.748 6.07-4.322 6.07-7.724z' fill='%231a1815' fill-rule='evenodd'/%3E%3C/svg%3E"

static int export_print_book(Book *bk, const char *imgd, const char *outdir,
                             double tw, double th, int pages, int bleed, int want_pdf) {
    (void)imgd;
    mkdir_p(outdir);
    double gutter = kdp_gutter(pages);
    double pw = tw + (bleed ? 0.125 : 0), ph = th + (bleed ? 0.25 : 0);
    double m_top = 0.75 + (bleed ? 0.125 : 0);
    double m_bot = 0.75 + (bleed ? 0.125 : 0);
    double m_out = 0.5 + (bleed ? 0.125 : 0);

    char hp[PATH_MAX];
    snprintf(hp, sizeof hp, "%s/print.html", outdir);
    FILE *f = fopen(hp, "w");
    if (!f) die("fopen print.html");
    char ffbuf[128];
    {
        const char *ff = font_family_for(bk->font);
        if (ff) {
            snprintf(ffbuf, sizeof ffbuf, "'%s', Georgia, serif", ff);
        } else {
            snprintf(ffbuf, sizeof ffbuf, "Georgia, 'Times New Roman', serif");
        }
    }
    const char *ffFamily = ffbuf;
    const char *lang = (bk->language && bk->language[0]) ? bk->language : "it";

    /* CSS per interruzioni di scena */
    const char *sb_css =
        "hr.sb,hr { border: none; margin: 2em 0; text-align: center; height: auto; }\n"
        "hr.sb::after,hr::after { content: '* * *'; display: block; letter-spacing: .6em; color: #1a1a1a; font-size: 1em; }\n"
        "hr + p, hr.sb + p { text-indent: 0; }\n";
    if (bk->scenebreak && !strcmp(bk->scenebreak, "fleuron")) {
        sb_css =
            "hr.sb,hr { border: none; margin: 2em 0; height: auto; }\n"
            "hr.sb::after,hr::after { content: ''; display: block; width: 7em; height: 24px; margin: 0 auto; "
            "background: url(\"data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 120 30'%3E%3Cpath d='M6 16 C 16 4 30 4 38 16 C 44 25 34 28 30 22 C 26 15 40 8 60 16 C 80 24 96 8 114 16' fill='none' stroke='%231c1a17' stroke-width='1.6' stroke-linecap='round'/%3E%3C/svg%3E\") no-repeat center / contain; }\n"
            "hr + p, hr.sb + p { text-indent: 0; }\n";
    } else if (bk->scenebreak && !strcmp(bk->scenebreak, "losanga")) {
        sb_css =
            "hr.sb,hr { border: none; margin: 2em 0; text-align: center; height: auto; }\n"
            "hr.sb::after,hr::after { content: '\\25c7'; display: block; color: #1c1a17; font-size: 1.05em; }\n"
            "hr + p, hr.sb + p { text-indent: 0; }\n";
    } else if (bk->scenebreak && !strcmp(bk->scenebreak, "linea")) {
        sb_css =
            "hr.sb,hr { border: none; border-top: 1px solid #b9b2a4; margin: 2.2em 5em; height: auto; }\n"
            "hr.sb::after,hr::after { content: '\\25c6'; display: block; width: 3em; margin: -0.72em auto 0; background: #fff; text-align: center; color: #1c1a17; font-size: .75em; }\n"
            "hr + p, hr.sb + p { text-indent: 0; }\n";
    }

    fprintf(f, "<!DOCTYPE html>\n<html lang=\"%s\">\n<head>\n<meta charset=\"utf-8\">\n<title>", lang);
    fprint_esc(f, bk->title ? bk->title : "Libro");
    fprintf(f, "</title>\n<style>\n"
               "@page {\n"
               "  size: %.3fin %.3fin;\n"
               "  margin-top: %.3fin;\n"
               "  margin-bottom: %.3fin;\n"
               "  @bottom-center {\n"
               "    content: counter(page);\n"
               "    font-size: 9pt;\n"
               "    color: #333;\n"
               "  }\n"
               "}\n"
               "@page frontmatter {\n"
               "  @bottom-center { content: none; }\n"
               "}\n"
               "@page :left { margin-left: %.3fin; margin-right: %.3fin; }\n"
               "@page :right { margin-left: %.3fin; margin-right: %.3fin; }\n"
               "body, .chapter, .chapter p, p, blockquote, li { margin: 0; padding: 0; font-family: %s; font-size: 11pt; "
               "line-height: 1.5; color: #000; }\n"
               "pre { margin: 1em 0; padding: 0.8em; background: #f6f6f6; border-radius: 4px; white-space: pre-wrap; word-break: break-all; }\n"
               "code { font-family: monospace; font-size: 0.9em; }\n"
               ".titlepage { page: frontmatter; page-break-after: always; text-align: center; padding-top: 2in; }\n"
               ".titlepage h1 { font-size: 24pt; }\n"
               ".copyright { page: frontmatter; page-break-after: always; font-size: 9pt; padding-top: 4in; }\n"
               ".toc { page: frontmatter; page-break-after: always; }\n"
               ".dedication { page: frontmatter; page-break-before: always; page-break-after: always; text-align: center; padding-top: 2.2in; font-style: italic; }\n"
               ".dedication h2 { display: none; }\n"
               ".dedication p { text-align: center; text-indent: 0; font-size: 11pt; line-height: 1.8; margin-bottom: 0.8em; }\n"
               ".epigraph { page: frontmatter; page-break-before: always; page-break-after: always; padding-top: 2in; width: 75%%; margin-left: auto; margin-right: auto; font-style: italic; }\n"
               ".epigraph h2 { display: none; }\n"
               ".epigraph p { text-align: justify; text-indent: 0; font-size: 10.5pt; line-height: 1.6; margin-bottom: 0.8em; }\n"
               ".epigraph p:last-of-type, .epigraph .cite { text-align: right; font-style: normal; font-size: 9.5pt; margin-top: 1em; }\n"
               ".about-author { page-break-before: always; }\n"
               ".about-author h2 { font-size: 16pt; text-align: center; font-variant: small-caps; break-after: avoid; page-break-after: avoid; margin-bottom: 1.2em; }\n"
               ".about-author figure { text-align: center; margin: 0 auto 1.2em; }\n"
               ".about-author figure img { width: 1.6in; height: 1.6in; border-radius: 50%%; object-fit: cover; display: inline-block; }\n"
               ".acknowledgments { page-break-before: always; }\n"
               ".acknowledgments h2 { font-size: 16pt; text-align: center; font-variant: small-caps; break-after: avoid; page-break-after: avoid; margin-bottom: 1.2em; }\n"
               ".chapter { page-break-before: always; }\n"
               ".chapter h2 { font-size: 16pt; text-align: center; font-variant: small-caps; break-after: avoid; page-break-after: avoid; }\n"
               ".chapter h3 { font-size: 13pt; margin-top: 1.2em; break-after: avoid; page-break-after: avoid; }\n"
               ".chapter h4 { font-size: 11.5pt; margin-top: 1em; break-after: avoid; page-break-after: avoid; font-weight: bold; }\n"
               ".chapter h5 { font-size: 10.5pt; margin-top: 0.8em; break-after: avoid; page-break-after: avoid; font-style: italic; }\n"
               "blockquote { margin: 1.2em 2em; padding-left: 1em; border-left: 2px solid #666; font-style: italic; }\n"
               "blockquote p { text-indent: 0; text-align: justify; }\n"
               "ul, ol { margin: 0.8em 0 0.8em 2em; padding: 0; }\n"
               "li { margin-bottom: 0.3em; text-align: justify; }\n"
               "p { text-align: justify; hyphens: auto; margin: 0 0 0.35em; text-indent: 1.5em; orphans: 2; widows: 2; }\n"
               "p.noindent, .chapter h2 + p, .chapter h3 + p, .chapter h4 + p, p.first-para, hr + p, hr.sb + p, blockquote + p, table + p, .table-wrap + p, table.book-table + p { text-indent: 0; }\n"
               ".table-wrap { margin: 1.4em auto; page-break-inside: avoid; break-inside: avoid; }\n"
               "table.book-table, table { width: 100%%; border-collapse: collapse; margin: 0 auto; font-size: 9.5pt; page-break-inside: avoid; break-inside: avoid; }\n"
               "table.book-table th, table.book-table td, table th, table td { padding: 5px 8px; border-bottom: 1px solid #ccc; vertical-align: top; }\n"
               "table.book-table th, table th { font-weight: bold; border-top: 1.5px solid #222; border-bottom: 1.5px solid #222; font-variant: small-caps; }\n"
               "table.book-table tr:last-child td, table tr:last-child td { border-bottom: 1.5px solid #222; }\n"
               "%s"
               ".dropcap { font-size: inherit; }\n"
               ".first-phrase { font-variant: small-caps; font-feature-settings: \"smcp\", \"c2sc\"; letter-spacing: .06em; font-weight: 500; }\n"
               "img { max-width: 100%%; }\n"
               "figure { text-align: center; }\n"
               "figcaption { font-size: 9pt; font-style: italic; }\n",
            pw, ph, m_top, m_bot, m_out, gutter, gutter, m_out,
            ffFamily, sb_css);
    char fnd_css[PATH_MAX];
    snprintf(fnd_css, sizeof fnd_css, "%s/fonts", outdir);
    emit_font_faces(f, fnd_css);
    emit_preset_style(f, bk->style);
    emit_custom_style(f, bk->vdir);
    if (bk->font && *bk->font) {
        fprintf(f, "body, .chapter, .chapter p, p, blockquote, li { font-family: %s !important; }\n", ffFamily);
    }
    fprintf(f, "</style>\n</head>\n<body>\n");
    fprintf(f, "<div class=\"titlepage\"><h1>");
    fprint_esc(f, bk->title ? bk->title : "Senza titolo");
    fprintf(f, "</h1><p style=\"text-align:center;text-indent:0\">");
    fprint_esc(f, bk->author ? bk->author : "");
    fprintf(f, "</p></div>\n"
               "<div class=\"copyright\"><p class=\"noindent\">");
    fprint_esc(f, bk->title ? bk->title : "");
    fprintf(f, " — Copyright &copy; ");
    fprint_esc(f, bk->author ? bk->author : "");
    fprintf(f, ". Tutti i diritti riservati.<br>"
               "Prima edizione. (Completa questa pagina prima della stampa.)</p></div>\n");
    fprintf(f, "<div class=\"toc\"><h2>Indice</h2>\n<ol>\n");
    for (size_t i = 0; i < bk->nch; i++) {
        if (bk->chs[i].type && (!strcmp(bk->chs[i].type, "dedication") || !strcmp(bk->chs[i].type, "epigraph")))
            continue;
        fprintf(f, "<li><a href=\"#ch%zu\">", i + 1);
        fprint_esc(f, bk->chs[i].title);
        fprintf(f, "</a></li>\n");
    }
    fprintf(f, "</ol></div>\n");
    for (size_t i = 0; i < bk->nch; i++) {
        int is_ded = bk->chs[i].type && !strcmp(bk->chs[i].type, "dedication");
        int is_epi = bk->chs[i].type && !strcmp(bk->chs[i].type, "epigraph");
        int is_auth = bk->chs[i].type && !strcmp(bk->chs[i].type, "about-author");
        int is_ack = bk->chs[i].type && !strcmp(bk->chs[i].type, "acknowledgments");
        const char *cls = "chapter";
        if (is_ded) cls = "dedication";
        else if (is_epi) cls = "epigraph";
        else if (is_auth) cls = "chapter about-author";
        else if (is_ack) cls = "chapter acknowledgments";

        fprintf(f, "<div class=\"%s\" id=\"ch%zu\">", cls, i + 1);
        if (!is_ded && !is_epi) {
            fprintf(f, "<h2>");
            fprint_esc(f, bk->chs[i].title);
            fprintf(f, "</h2>\n");
        }
        int first = 1, subn = 0;
        ListState ls = {0};
        int in_bq = 0;
        for (size_t j = 0; j < bk->chs[i].nseg; j++) {
            Seg *s = &bk->chs[i].segs[j];
            if (s->type != SEG_UL_ITEM && s->type != SEG_OL_ITEM) { list_close_all(f, &ls); }
            if (s->type != SEG_BLOCKQUOTE && in_bq) { fprintf(f, "</blockquote>\n"); in_bq = 0; }

            if (s->type == SEG_SUBHEAD) {
                subn++;
                fprintf(f, "<h3 id=\"ch%zu-s%d\">", i + 1, subn);
                fprint_md_inline(f, s->text ? s->text : "");
                fprintf(f, "</h3>\n");
                first = 1;
            } else if (s->type == SEG_H3) {
                fprintf(f, "<h4>");
                fprint_md_inline(f, s->text ? s->text : "");
                fprintf(f, "</h4>\n");
                first = 1;
            } else if (s->type == SEG_H4) {
                fprintf(f, "<h5>");
                fprint_md_inline(f, s->text ? s->text : "");
                fprintf(f, "</h5>\n");
                first = 1;
            } else if (s->type == SEG_BLOCKQUOTE) {
                if (!in_bq) {
                    fprintf(f, "<blockquote>\n");
                    in_bq = 1;
                }
                fprintf(f, "<p>");
                fprint_md_inline(f, s->text ? s->text : "");
                fprintf(f, "</p>\n");
                first = 0;
            } else if (s->type == SEG_UL_ITEM) {
                list_handle_item(f, &ls, SEG_UL_ITEM, s->level, s->num, s->text);
                first = 0;
            } else if (s->type == SEG_OL_ITEM) {
                list_handle_item(f, &ls, SEG_OL_ITEM, s->level, s->num, s->text);
                first = 0;
            } else if (s->type == SEG_CODE_BLOCK) {
                fprintf(f, "<pre><code>");
                fprint_esc(f, s->text ? s->text : "");
                fprintf(f, "</code></pre>\n");
                first = 0;
            } else if (s->type == SEG_SCENE_BREAK) {
                fprintf(f, "<hr class=\"sb\"/>\n");
                first = 1;
            } else if (s->type == SEG_TABLE) {
                fprint_table_html(f, s->text);
                first = 0;
            } else if (s->type == SEG_IMAGE) {
                if (s->text) {
                    fprintf(f, "<figure><img src=\"images/");
                    fprint_esc(f, s->text);
                    fprintf(f, "\" alt=\"");
                    fprint_esc(f, s->caption ? s->caption : "");
                    fprintf(f, "\">");
                    if (s->caption && s->caption[0]) {
                        fprintf(f, "<figcaption>");
                        fprint_md_inline(f, s->caption);
                        fprintf(f, "</figcaption>");
                    }
                    fprintf(f, "</figure>\n");
                }
                first = 0;
            } else if (s->text) {
                const char *pp = s->text;
                int fp = first;
                while (*pp) {
                    while (*pp == '\n' || *pp == '\r') pp++;
                    if (!*pp) break;
                    const char *e = strchr(pp, '\n');
                    size_t L = e ? (size_t)(e - pp) : strlen(pp);
                    while (L && (pp[L-1] == '\r' || pp[L-1] == ' ' || pp[L-1] == '\t')) L--;
                    if (L) {
                        char *t2 = malloc(L + 1);
                        memcpy(t2, pp, L); t2[L] = 0;
                        if (is_scene_break(t2)) {
                            fprintf(f, "<hr class=\"sb\"/>\n");
                            fp = 1;
                        } else if (is_ded || is_epi) {
                            fprintf(f, "<p class=\"noindent\">");
                            fprint_md_inline(f, t2);
                            fprintf(f, "</p>\n");
                        } else if (fp) {
                            fprintf(f, "<p class=\"noindent first-para\">");
                            if (bk->opening && !strcmp(bk->opening, "smallcaps")) {
                                fprint_esc_with_first_phrase(f, t2);
                            } else if (bk->opening && !strcmp(bk->opening, "none")) {
                                fprint_md_inline(f, t2);
                            } else {
                                fprint_esc_with_dropcap(f, t2);
                            }
                            fprintf(f, "</p>\n");
                            fp = 0;
                        } else {
                            fprintf(f, "<p>");
                            fprint_md_inline(f, t2);
                            fprintf(f, "</p>\n");
                        }
                        free(t2);
                    }
                    pp = e ? e + 1 : pp + L;
                }
                first = 0;
            }
        }
        list_close_all(f, &ls);
        if (in_bq) fprintf(f, "</blockquote>\n");
        fprintf(f, "</div>\n");
    }
    fprintf(f, "</body>\n</html>\n");
    fclose(f);
    printf("HTML di stampa in %s/print.html (trim %.3fx%.3f in%s, %zu cap., gutter %.3f in per ~%d pag.)\n",
           outdir, tw, th, bleed ? " +bleed" : "", bk->nch, gutter, pages);

    if (!want_pdf) {
        printf("Per il PDF: apri print.html in Safari/Chrome -> Stampa -> Salva come PDF\n"
               "con formato pagina personalizzato %.3f x %.3f in.\n", pw, ph);
        return 0;
    }
    char pdf[PATH_MAX];
    snprintf(pdf, sizeof pdf, "%s/book.pdf", outdir);
    char *qh = sh_quote(hp), *qp = sh_quote(pdf);
    char cmd[PATH_MAX * 3];
    const char *how = NULL;
    const char *chrom_env = getenv("CHROMIUM_BIN");
    if (chrom_env && chrom_env[0] && access(chrom_env, X_OK) == 0) {
        snprintf(cmd, sizeof cmd, "\"%s\" --headless=new --no-sandbox --disable-gpu --no-pdf-header-footer --run-all-compositor-stages-before-draw --virtual-time-budget=5000 --print-to-pdf=%s %s >/dev/null 2>&1", chrom_env, qp, qh);
        how = chrom_env;
    } else
#ifdef __APPLE__
    if (access("/Applications/Brave Browser.app/Contents/MacOS/Brave Browser", X_OK) == 0) {
        snprintf(cmd, sizeof cmd, "\"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser\" --headless=new --no-sandbox --disable-gpu --no-pdf-header-footer --run-all-compositor-stages-before-draw --virtual-time-budget=5000 --print-to-pdf=%s %s >/dev/null 2>&1", qp, qh);
        how = "Brave Browser";
    } else if (access("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", X_OK) == 0) {
        snprintf(cmd, sizeof cmd, "\"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome\" --headless=new --no-sandbox --disable-gpu --no-pdf-header-footer --run-all-compositor-stages-before-draw --virtual-time-budget=5000 --print-to-pdf=%s %s >/dev/null 2>&1", qp, qh);
        how = "Google Chrome";
    } else if (access("/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge", X_OK) == 0) {
        snprintf(cmd, sizeof cmd, "\"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge\" --headless=new --no-sandbox --disable-gpu --no-pdf-header-footer --run-all-compositor-stages-before-draw --virtual-time-budget=5000 --print-to-pdf=%s %s >/dev/null 2>&1", qp, qh);
        how = "Microsoft Edge";
    } else
#endif
    if (system("which brave-browser >/dev/null 2>&1") == 0) {
        snprintf(cmd, sizeof cmd, "brave-browser --headless=new --no-sandbox --disable-gpu --no-pdf-header-footer --run-all-compositor-stages-before-draw --virtual-time-budget=5000 --print-to-pdf=%s %s >/dev/null 2>&1 || brave-browser --headless --no-sandbox --print-to-pdf=%s %s >/dev/null 2>&1", qp, qh, qp, qh);
        how = "brave-browser";
    } else if (system("which google-chrome >/dev/null 2>&1") == 0) {
        snprintf(cmd, sizeof cmd, "google-chrome --headless=new --no-sandbox --disable-gpu --no-pdf-header-footer --run-all-compositor-stages-before-draw --virtual-time-budget=5000 --print-to-pdf=%s %s >/dev/null 2>&1 || google-chrome --headless --no-sandbox --print-to-pdf=%s %s >/dev/null 2>&1", qp, qh, qp, qh);
        how = "google-chrome";
    } else if (system("which google-chrome-stable >/dev/null 2>&1") == 0) {
        snprintf(cmd, sizeof cmd, "google-chrome-stable --headless=new --no-sandbox --disable-gpu --no-pdf-header-footer --run-all-compositor-stages-before-draw --virtual-time-budget=5000 --print-to-pdf=%s %s >/dev/null 2>&1 || google-chrome-stable --headless --no-sandbox --print-to-pdf=%s %s >/dev/null 2>&1", qp, qh, qp, qh);
        how = "google-chrome-stable";
    } else if (system("which chromium >/dev/null 2>&1") == 0) {
        snprintf(cmd, sizeof cmd, "chromium --headless=new --no-sandbox --disable-gpu --no-pdf-header-footer --run-all-compositor-stages-before-draw --virtual-time-budget=5000 --print-to-pdf=%s %s >/dev/null 2>&1 || chromium --headless --no-sandbox --print-to-pdf=%s %s >/dev/null 2>&1", qp, qh, qp, qh);
        how = "chromium";
    } else if (system("which chromium-browser >/dev/null 2>&1") == 0) {
        snprintf(cmd, sizeof cmd, "chromium-browser --headless=new --no-sandbox --disable-gpu --no-pdf-header-footer --run-all-compositor-stages-before-draw --virtual-time-budget=5000 --print-to-pdf=%s %s >/dev/null 2>&1 || chromium-browser --headless --no-sandbox --print-to-pdf=%s %s >/dev/null 2>&1", qp, qh, qp, qh);
        how = "chromium-browser";
    } else if (system("which msedge >/dev/null 2>&1") == 0) {
        snprintf(cmd, sizeof cmd, "msedge --headless=new --no-sandbox --disable-gpu --no-pdf-header-footer --run-all-compositor-stages-before-draw --virtual-time-budget=5000 --print-to-pdf=%s %s >/dev/null 2>&1 || msedge --headless --no-sandbox --print-to-pdf=%s %s >/dev/null 2>&1", qp, qh, qp, qh);
        how = "msedge";
    } else if (system("python3 -c 'import weasyprint' >/dev/null 2>&1") == 0) {
        snprintf(cmd, sizeof cmd, "python3 -m weasyprint %s %s", qh, qp);
        how = "weasyprint";
    } else if (system("which wkhtmltopdf >/dev/null 2>&1") == 0) {
        snprintf(cmd, sizeof cmd, "wkhtmltopdf --enable-local-file-access --page-width %.3fin --page-height %.3fin -T 0 -B 0 -L 0 -R 0 %s %s >/dev/null 2>&1",
                 pw, ph, qh, qp);
        how = "wkhtmltopdf";
    } else {
        free(qh); free(qp);
        fprintf(stderr, "nessun motore PDF installato (chromium/chrome/brave/edge/weasyprint/wkhtmltopdf).\n"
                        "Apri print.html nel browser e usa Stampa -> Salva come PDF.\n");
        return 1;
    }
    free(qh); free(qp);
    int rc = run_cmd_status(cmd);
    struct stat st;
    if (rc == 0 && stat(pdf, &st) == 0 && st.st_size > 0) {
        printf("PDF generato con %s: %s\n", how, pdf);
        int real_pages = count_pdf_pages(pdf);
        if (real_pages > 0 && kdp_gutter(real_pages) != gutter) {
            printf("Ricalcolo gutter KDP sulle pagine effettive (%d pag., gutter %.3f in anziche' %.3f in)...\n",
                   real_pages, kdp_gutter(real_pages), gutter);
            return export_print_book(bk, imgd, outdir, tw, th, real_pages, bleed, want_pdf);
        }
    } else {
        fprintf(stderr, "errore: il motore %s ha fallito la generazione del PDF (rc=%d)\n", how, rc);
        rc = 1;
    }
    return rc;
}

/* interruzione di scena: riga fatta solo di *, -, _ e spazi (***, * * *, ---, ___) */
static int is_scene_break(const char *s) {
    if (!s) return 0;
    int stars = 0, dashes = 0, unders = 0;
    for (const char *p = s; *p; p++) {
        if (*p == '*') stars++;
        else if (*p == '-') dashes++;
        else if (*p == '_') unders++;
        else if (*p == ' ' || *p == '\t') continue;
        else return 0;
    }
    if (stars >= 3 && dashes == 0 && unders == 0) return 1;
    if (dashes >= 3 && stars == 0 && unders == 0) return 1;
    if (unders >= 3 && stars == 0 && dashes == 0) return 1;
    return 0;
}

static int export_print(const char *archive_path, const char *outdir,
                        double tw, double th, int pages, int bleed, int want_pdf) {
    char *tmp = mk_tmpdir();
    Book bk;
    int rc = load_book(archive_path, tmp, &bk);
    if (rc == 0) {
        char imgd[PATH_MAX];
        snprintf(imgd, sizeof imgd, "%s/images", outdir);
        copy_images(tmp, imgd);
        resolve_archive_images(&bk, imgd);
        rc = export_print_book(&bk, imgd, outdir, tw, th, pages, bleed, want_pdf);
    }
    rm_rf_path(tmp);
    free(tmp);
    return rc;
}

/* ================= vault Markdown (compatibile Obsidian) ================= */
/* slug per nomi file: minuscolo, accenti piegati, resto -> '-' */
static void slugify(const char *in, char *out, size_t cap) {
    size_t o = 0;
    /* piega UTF-8 comuni (italiano/francese/tedesco/spagnolo) */
    for (const unsigned char *p = (const unsigned char *)in; *p && o + 2 < cap; ) {
        unsigned char c = *p;
        if (c < 0x80) {
            if (isalnum(c)) out[o++] = (char)tolower(c);
            else if (o && out[o-1] != '-') out[o++] = '-';
            p++;
        } else if (c == 0xC3 && p[1]) {
            unsigned char d = p[1];
            char m = 0;
            if (strchr("\xE0\xE1\xE2\xE4\xE3\xE5\xAA", (char)d)) m = 'a';
            else if (strchr("\xE8\xE9\xEA\xEB", (char)d)) m = 'e';
            else if (strchr("\xEC\xED\xEE\xEF", (char)d)) m = 'e' - 'e' + 'i';
            else if (strchr("\xF2\xF3\xF4\xF6\xF5\xB2", (char)d)) m = 'o';
            else if (strchr("\xF9\xFA\xFB\xFC", (char)d)) m = 'u';
            else if (d == 0xA7) m = 'c';
            else if (d == 0xB1) m = 'n';
            if (m) out[o++] = m;
            else if (o && out[o-1] != '-') out[o++] = '-';
            p += 2;
        } else if (c == 0xE2 && p[1] == 0x80 && p[2]) {
            /* punteggiatura 2000-206F -> '-' (una sola) */
            if (o && out[o-1] != '-') out[o++] = '-';
            p += 3;
        } else {
            if (o && out[o-1] != '-') out[o++] = '-';
            p++;
            while ((*p & 0xC0) == 0x80) p++;
        }
    }
    while (o && out[o-1] == '-') o--;
    out[o] = 0;
    if (!o) snprintf(out, cap, "capitolo");
}
static void trim_inplace(char *s) {
    while (*s == ' ' || *s == '\t' || *s == '\r' || *s == '\n') memmove(s, s + 1, strlen(s));
    size_t L = strlen(s);
    while (L && (s[L-1] == ' ' || s[L-1] == '\t' || s[L-1] == '\r' || s[L-1] == '\n')) s[--L] = 0;
}
static void clean_yaml_val(char *s) {
    trim_inplace(s);
    size_t L = strlen(s);
    if (!L) return;

    if (s[0] == '"' || s[0] == '\'') {
        const char quote = s[0];
        char *end_q = NULL;
        for (char *p = s + 1; *p; p++) {
            if (*p == '\\' && quote == '"' && *(p + 1)) {
                p++;
            } else if (*p == quote) {
                if (quote == '\'' && *(p + 1) == '\'') {
                    p++;
                } else {
                    end_q = p;
                    break;
                }
            }
        }
        if (end_q) {
            *end_q = 0;
            memmove(s, s + 1, (size_t)(end_q - s));
            char *read = s, *write = s;
            while (*read) {
                if (quote == '\'' && read[0] == '\'' && read[1] == '\'') {
                    *write++ = '\''; read += 2;
                } else if (quote == '"' && read[0] == '\\' && read[1]) {
                    read++;
                    if (*read == 'n') { *write++ = '\n'; read++; }
                    else if (*read == 'r') { *write++ = '\r'; read++; }
                    else if (*read == 't') { *write++ = '\t'; read++; }
                    else if (*read == 'u' && isxdigit((unsigned char)read[1]) && isxdigit((unsigned char)read[2]) &&
                             isxdigit((unsigned char)read[3]) && isxdigit((unsigned char)read[4])) {
                        char hbuf[5] = { read[1], read[2], read[3], read[4], 0 };
                        unsigned int cp = (unsigned int)strtoul(hbuf, NULL, 16);
                        read += 5;
                        if (cp <= 0x7F) {
                            *write++ = (char)cp;
                        } else if (cp <= 0x7FF) {
                            *write++ = (char)(0xC0 | ((cp >> 6) & 0x1F));
                            *write++ = (char)(0x80 | (cp & 0x3F));
                        } else {
                            *write++ = (char)(0xE0 | ((cp >> 12) & 0x0F));
                            *write++ = (char)(0x80 | ((cp >> 6) & 0x3F));
                            *write++ = (char)(0x80 | (cp & 0x3F));
                        }
                    } else if (*read == 'x' && isxdigit((unsigned char)read[1]) && isxdigit((unsigned char)read[2])) {
                        char hbuf[3] = { read[1], read[2], 0 };
                        unsigned int cp = (unsigned int)strtoul(hbuf, NULL, 16);
                        read += 3;
                        *write++ = (char)cp;
                    } else {
                        *write++ = *read++;
                    }
                } else {
                    *write++ = *read++;
                }
            }
            *write = 0;
            return;
        }
    }

    char *c = strstr(s, " #");
    if (c) {
        *c = 0;
        trim_inplace(s);
    }
}

/* ---- scrittura vault ---- */
static int export_md(const char *archive_path, const char *vdir) {
    char *tmp = mk_tmpdir();
    Book bk;
    int rc = load_book(archive_path, tmp, &bk);
    if (rc == 0) {
    mkdir_p(vdir);
    char imgd[PATH_MAX];
    snprintf(imgd, sizeof imgd, "%s/images", vdir);
    copy_images(tmp, imgd);
    resolve_archive_images(&bk, imgd);
    /* nome file immagine per imageKey */
    char yml[PATH_MAX];
    snprintf(yml, sizeof yml, "%s/book.yaml", vdir);
    FILE *y = fopen(yml, "w");
    if (!y) die("fopen book.yaml");
    fprintf(y, "# PubCraft vault - apri questa cartella con Obsidian (Open folder as vault)\n");
    fprintf(y, "title: %s\n", bk.title ? bk.title : "");
    fprintf(y, "author: %s\n", bk.author ? bk.author : "");
    fprintf(y, "language: it\n");
    if (bk.bookuuid) fprintf(y, "uuid: %s\n", bk.bookuuid);
    fprintf(y, "chapters:\n");
    for (size_t i = 0; i < bk.nch; i++) {
        char slug[128];
        slugify(bk.chs[i].title ? bk.chs[i].title : "capitolo", slug, sizeof slug);
        char fn[160];
        snprintf(fn, sizeof fn, "%02zu-%s.md", i + 1, slug);
        fprintf(y, "- file: %s\n", fn);
        fprintf(y, "  id: %s\n", bk.chs[i].id ? bk.chs[i].id : "");
        fprintf(y, "  title: %s\n", bk.chs[i].title ? bk.chs[i].title : "");
        char mp[PATH_MAX];
        snprintf(mp, sizeof mp, "%s/%s", vdir, fn);
        FILE *f = fopen(mp, "w");
        if (!f) die("fopen md");
        fprintf(f, "---\n");
        if (bk.chs[i].id) fprintf(f, "id: %s\n", bk.chs[i].id);
        if (bk.chs[i].title) fprintf(f, "title: %s\n", bk.chs[i].title);
        fprintf(f, "---\n\n");
        for (size_t j = 0; j < bk.chs[i].nseg; j++) {
            Seg *s = &bk.chs[i].segs[j];
            if (s->type == SEG_SUBHEAD) {
                fprintf(f, "## %s\n\n", s->text ? s->text : "");
            } else if (s->type == SEG_TABLE) {
                fprintf(f, "%s\n\n", s->text ? s->text : "");
            } else if (s->type == SEG_IMAGE) {
                if (s->text)
                    fprintf(f, "![%s](images/%s)\n\n",
                            s->caption ? s->caption : "", s->text);
            } else if (s->text) {
                const char *pp = s->text;
                while (*pp) {
                    while (*pp == '\n' || *pp == '\r') pp++;
                    if (!*pp) break;
                    const char *e = strchr(pp, '\n');
                    size_t L = e ? (size_t)(e - pp) : strlen(pp);
                    while (L && (pp[L-1] == '\r' || pp[L-1] == ' ' || pp[L-1] == '\t')) L--;
                    if (L) {
                        if (L >= 3 && (!strncmp(pp, "## ", 3) || !strncmp(pp, "![", 2)))
                            fputc('\\', f);
                        fwrite(pp, 1, L, f);
                        fprintf(f, "\n\n");
                    }
                    pp = e ? e + 1 : pp + L;
                }
            }
        }
        fclose(f);
    }
    fclose(y);
    printf("Vault scritto in %s/ (%zu capitoli, book.yaml + markdown)\n", vdir, bk.nch);
    }
    rm_rf_path(tmp);
    free(tmp);
    return rc;
}

/* ---- lettura vault ---- */
typedef struct { char *file, *id, *title, *type; } ChEntry;
static int load_vault(const char *vdir, Book *bk) {
    memset(bk, 0, sizeof *bk);
    bk->vdir = dupstr(vdir);
    char yml[PATH_MAX];
    snprintf(yml, sizeof yml, "%s/book.yaml", vdir);
    size_t n = 0;
    char *xy = (char *)read_file(yml, &n);
    if (!xy) { fprintf(stderr, "errore: %s assente (non e' un vault)\n", yml); return -1; }
    ChEntry *ents = NULL;
    size_t nen = 0, capen = 0;
    ChEntry *cur_e = NULL;
    int in_ch = 0;
    char *line = strtok(xy, "\n");
    while (line) {
        int indent = 0;
        char *l = line;
        while (*l == ' ' || *l == '\t') {
            if (*l == '\t') indent += 2;
            else indent++;
            l++;
        }
        while (*l == '\r') l++;
        if (!*l || *l == '#') { line = strtok(NULL, "\n"); continue; }
        if (!strcmp(l, "chapters:")) {
            in_ch = 1;
        } else if (in_ch && indent == 0 && *l != '-') {
            in_ch = 0;
        }

        if (in_ch) {
            int is_item_start = 0;
            char *item_line = l;
            if (item_line[0] == '-' && (item_line[1] == ' ' || item_line[1] == '\t')) {
                is_item_start = 1;
                item_line += 2;
                while (*item_line == ' ' || *item_line == '\t') item_line++;
            }
            if (is_item_start) {
                if (nen == capen) {
                    capen = capen ? capen * 2 : 16;
                    ents = realloc(ents, capen * sizeof *ents);
                }
                cur_e = &ents[nen++];
                memset(cur_e, 0, sizeof *cur_e);
            }

            if (cur_e) {
                char *key_line = is_item_start ? item_line : l;
                if (!strncmp(key_line, "file:", 5)) {
                    char *v = strchr(key_line, ':') + 1;
                    clean_yaml_val(v);
                    free(cur_e->file);
                    cur_e->file = dupstr(v);
                } else if (!strncmp(key_line, "id:", 3)) {
                    char *v = strchr(key_line, ':') + 1;
                    clean_yaml_val(v);
                    free(cur_e->id);
                    cur_e->id = dupstr(v);
                } else if (!strncmp(key_line, "title:", 6)) {
                    char *v = strchr(key_line, ':') + 1;
                    clean_yaml_val(v);
                    free(cur_e->title);
                    cur_e->title = dupstr(v);
                } else if (!strncmp(key_line, "type:", 5)) {
                    char *v = strchr(key_line, ':') + 1;
                    clean_yaml_val(v);
                    free(cur_e->type);
                    cur_e->type = dupstr(v);
                }
            }
        } else if (!in_ch && !strncmp(l, "title:", 6)) {
            char *v = l + 6;
            clean_yaml_val(v);
            free(bk->title);
            bk->title = dupstr(v);
        } else if (!in_ch && !strncmp(l, "author:", 7)) {
            char *v = l + 7;
            clean_yaml_val(v);
            free(bk->author);
            bk->author = dupstr(v);
        } else if (!in_ch && !strncmp(l, "language:", 9)) {
            char *v = l + 9;
            clean_yaml_val(v);
            free(bk->language);
            bk->language = dupstr(v);
        } else if (!in_ch && !strncmp(l, "uuid:", 5)) {
            char *v = l + 5;
            clean_yaml_val(v);
            free(bk->bookuuid);
            bk->bookuuid = dupstr(v);
        } else if (!in_ch && !strncmp(l, "cover:", 6)) {
            char *v = l + 6;
            clean_yaml_val(v);
            free(bk->cover);
            bk->cover = dupstr(v);
        } else if (!in_ch && !strncmp(l, "style:", 6)) {
            char *v = l + 6;
            clean_yaml_val(v);
            free(bk->style);
            bk->style = dupstr(v);
        } else if (!in_ch && !strncmp(l, "opening:", 8)) {
            char *v = l + 8;
            clean_yaml_val(v);
            free(bk->opening);
            bk->opening = dupstr(v);
        } else if (!in_ch && !strncmp(l, "font:", 5)) {
            char *v = l + 5;
            clean_yaml_val(v);
            free(bk->font);
            bk->font = dupstr(v);
        } else if (!in_ch && !strncmp(l, "scenebreak:", 11)) {
            char *v = l + 11;
            clean_yaml_val(v);
            free(bk->scenebreak);
            bk->scenebreak = dupstr(v);
        } else if (!in_ch && !strncmp(l, "trim:", 5)) {
            char *v = l + 5;
            clean_yaml_val(v);
            free(bk->trim);
            bk->trim = dupstr(v);
        } else if (!in_ch && !strncmp(l, "pages:", 6)) {
            char *v = l + 6;
            clean_yaml_val(v);
            bk->pages = atoi(v);
        } else if (!in_ch && !strncmp(l, "bleed:", 6)) {
            char *v = l + 6;
            clean_yaml_val(v);
            if (!strcasecmp(v, "true") || !strcasecmp(v, "yes") || !strcmp(v, "1"))
                bk->bleed = 1;
            else
                bk->bleed = 0;
        }
        line = strtok(NULL, "\n");
    }
    for (size_t i = 0; i < nen; i++) {
        char mp[PATH_MAX];
        snprintf(mp, sizeof mp, "%s/%s", vdir, ents[i].file);
        size_t mn = 0;
        char *md = (char *)read_file(mp, &mn);
        if (!md) {
            fprintf(stderr, "errore fatale: capitolo '%s' non trovato (%s)\n", ents[i].file, mp);
            for (size_t k = 0; k < nen; k++) {
                free(ents[k].file); free(ents[k].id); free(ents[k].title); free(ents[k].type);
            }
            free(ents);
            free(xy);
            return 1;
        }
        char *fmid = NULL, *fmtitle = NULL, *fmtype = NULL;
        char *body = md;
        if (!strncmp(md, "---", 3)) {
            char *e = strstr(md + 3, "\n---");
            if (e) {
                *e = 0;
                for (char *l = strtok(md + 3, "\n"); l; l = strtok(NULL, "\n")) {
                    if (!strncmp(l, "id:", 3)) { clean_yaml_val(l + 3); fmid = dupstr(l + 3); }
                    else if (!strncmp(l, "title:", 6)) { clean_yaml_val(l + 6); fmtitle = dupstr(l + 6); }
                    else if (!strncmp(l, "type:", 5)) { clean_yaml_val(l + 5); fmtype = dupstr(l + 5); }
                }
                body = e + 4;
            }
        }
        const char *cid = ents[i].id && ents[i].id[0] ? ents[i].id : fmid;
        const char *ctitle = ents[i].title && ents[i].title[0] ? ents[i].title
                             : (fmtitle ? fmtitle : "Senza titolo");
        const char *ctype = ents[i].type && ents[i].type[0] ? ents[i].type : (fmtype ? fmtype : NULL);
        if (!ctype || !*ctype) {
            char low_f[256], low_t[256];
            snprintf(low_f, sizeof low_f, "%s", ents[i].file ? ents[i].file : "");
            for (char *p = low_f; *p; p++) *p = tolower((unsigned char)*p);
            snprintf(low_t, sizeof low_t, "%s", ctitle ? ctitle : "");
            for (char *p = low_t; *p; p++) *p = tolower((unsigned char)*p);

            if (strstr(low_f, "dedica") || strstr(low_f, "dedication") || strstr(low_t, "dedica") || strstr(low_t, "dedication"))
                ctype = "dedication";
            else if (strstr(low_f, "epigra") || strstr(low_f, "epigraph") || strstr(low_t, "epigra") || strstr(low_t, "epigraph"))
                ctype = "epigraph";
            else if (strstr(low_f, "autore") || strstr(low_f, "about") || strstr(low_t, "autore") || strstr(low_t, "about"))
                ctype = "about-author";
            else if (strstr(low_f, "ringrazia") || strstr(low_f, "acknowledg") || strstr(low_t, "ringrazia"))
                ctype = "acknowledgments";
            else if (strstr(low_f, "prefazio") || strstr(low_f, "preface") || strstr(low_f, "foreword") || strstr(low_t, "prefazio"))
                ctype = "preface";
            else
                ctype = "chapter";
        }
        Chapter *c = book_new_chapter(bk, cid ? cid : "", ctitle);
        c->file = dupstr(ents[i].file);
        c->type = dupstr(ctype);
        /* corpo: sottotitoli, immagini, liste, citazioni, scene break, paragrafi */
        char *para = NULL;
        size_t plen = 0, plcap = 0;
#define FLUSH_PARA() do { \
    if (plen) { \
        while (plen && (para[plen-1] == ' ' || para[plen-1] == '\t' || para[plen-1] == '\r' || para[plen-1] == '\n')) para[--plen] = 0; \
        if (para[0]) chap_add_seg(c, SEG_TEXT, para, NULL, NULL); \
        plen = 0; if (para) para[0] = 0; \
    } \
} while (0)
        for (char *lp = body; ; ) {
            char *e = strchr(lp, '\n');
            if (e) *e = 0;
            char *t = lp;
            while (*t == '\r') t++;
            char *e2 = t + strlen(t);
            while (e2 > t && (e2[-1] == ' ' || e2[-1] == '\t' || e2[-1] == '\r')) e2--;
            *e2 = 0;
            int has_next = (e != NULL);
            if (!t[0]) { FLUSH_PARA(); if (!has_next) break; lp = e + 1; continue; }
            if (t[0] == '\\' && (t[1] == '#' || t[1] == '!')) t++; /* unescape writer */
            if (!strncmp(t, "```", 3)) {
                FLUSH_PARA();
                char *code = NULL;
                size_t clen = 0, ccap = 0;
                int nlines = 0;
                while (has_next) {
                    lp = e + 1;
                    e = strchr(lp, '\n');
                    if (e) *e = 0;
                    char *lt = lp;
                    while (*lt == '\r') lt++;
                    char *le = lt + strlen(lt);
                    while (le > lt && (le[-1] == '\r')) le--;
                    *le = 0;
                    has_next = (e != NULL);
                    if (!strncmp(lt, "```", 3)) {
                        break;
                    }
                    size_t ll = strlen(lt);
                    if (clen + ll + 2 > ccap) {
                        ccap = clen + ll + 256;
                        code = realloc(code, ccap);
                    }
                    if (nlines > 0) code[clen++] = '\n';
                    memcpy(code + clen, lt, ll);
                    clen += ll;
                    code[clen] = 0;
                    nlines++;
                }
                chap_add_seg(c, SEG_CODE_BLOCK, code ? code : "", NULL, NULL);
                free(code);
                if (!has_next) break;
                lp = e + 1;
                continue;
            } else if (has_next && strchr(t, '|') != NULL && is_table_start(t, e + 1)) {
                FLUSH_PARA();
                char *table_buf = NULL;
                size_t t_len = 0, t_cap = 0;
                size_t tl = strlen(t);
                t_cap = tl + 512;
                table_buf = malloc(t_cap);
                if (table_buf) {
                    memcpy(table_buf, t, tl);
                    t_len = tl;
                    table_buf[t_len] = 0;

                    while (has_next) {
                        lp = e + 1;
                        e = strchr(lp, '\n');
                        if (e) *e = 0;
                        char *row = lp;
                        while (*row == '\r') row++;
                        char *row_end = row + strlen(row);
                        while (row_end > row && (row_end[-1] == ' ' || row_end[-1] == '\t' || row_end[-1] == '\r')) row_end--;
                        *row_end = 0;
                        has_next = (e != NULL);

                        if (strchr(row, '|') == NULL || !row[0]) {
                            if (row[0]) {
                                size_t L = strlen(row);
                                if (plen + L + 2 > plcap) {
                                    plcap = plen + L + 64;
                                    para = realloc(para, plcap);
                                }
                                if (plen) para[plen++] = '\n';
                                memcpy(para + plen, row, L);
                                plen += L; para[plen] = 0;
                            }
                            break;
                        }
                        size_t rl = strlen(row);
                        if (t_len + rl + 2 > t_cap) {
                            t_cap = t_len + rl + 512;
                            table_buf = realloc(table_buf, t_cap);
                        }
                        table_buf[t_len++] = '\n';
                        memcpy(table_buf + t_len, row, rl);
                        t_len += rl;
                        table_buf[t_len] = 0;
                    }
                    chap_add_seg(c, SEG_TABLE, table_buf, NULL, NULL);
                    free(table_buf);
                }
                if (!has_next) break;
                lp = e + 1;
                continue;
            } else if (is_scene_break(t)) {
                FLUSH_PARA();
                chap_add_seg(c, SEG_SCENE_BREAK, t, NULL, NULL);
            } else if (!strncmp(t, "#### ", 5)) {
                FLUSH_PARA();
                chap_add_seg(c, SEG_H4, t + 5, NULL, NULL);
            } else if (!strncmp(t, "### ", 4)) {
                FLUSH_PARA();
                chap_add_seg(c, SEG_H3, t + 4, NULL, NULL);
            } else if (!strncmp(t, "## ", 3)) {
                FLUSH_PARA();
                chap_add_seg(c, SEG_SUBHEAD, t + 3, NULL, NULL);
            } else if (!strncmp(t, "# ", 2)) {
                FLUSH_PARA();
                const char *h1_title = t + 2;
                while (*h1_title == ' ' || *h1_title == '\t') h1_title++;
                if (c->nseg == 0 && (!c->title || !strcmp(c->title, "Senza titolo") || !strcasecmp(c->title, h1_title))) {
                    free(c->title);
                    c->title = dupstr(h1_title);
                } else {
                    chap_add_seg(c, SEG_SUBHEAD, h1_title, NULL, NULL);
                }
            } else if (!strncmp(t, ">", 1)) {
                FLUSH_PARA();
                const char *qp = t + 1;
                if (*qp == ' ') qp++;
                chap_add_seg(c, SEG_BLOCKQUOTE, qp, NULL, NULL);
            } else {
                const char *lt = t;
                while (*lt == ' ' || *lt == '\t') lt++;
                int indent = 0;
                for (const char *ip = t; ip < lt; ip++) {
                    if (*ip == '\t') indent += 2;
                    else indent++;
                }
                int level = indent >= 2 ? (indent / 2) : 0;
                if (level > 8) level = 8;
                if ((lt[0] == '-' || lt[0] == '*' || lt[0] == '+') && lt[1] == ' ') {
                    FLUSH_PARA();
                    chap_add_seg_full(c, SEG_UL_ITEM, lt + 2, NULL, NULL, 0, level);
                } else if (isdigit((unsigned char)lt[0])) {
                    const char *dp = lt;
                    long start_num = strtol(lt, (char**)&dp, 10);
                    if (*dp == '.' && dp[1] == ' ') {
                        FLUSH_PARA();
                        chap_add_seg_full(c, SEG_OL_ITEM, dp + 2, NULL, NULL, (int)start_num, level);
                    } else goto text_para;
                } else if (!strncmp(t, "![", 2)) {
                    FLUSH_PARA();
                    char *cb = strchr(t, ']');
                    char *op = cb ? strchr(cb, '(') : NULL;
                    char *cl = op ? strchr(op, ')') : NULL;
                    if (cb && op && cl && op > cb) {
                        *cb = 0; *cl = 0;
                        const char *cap = t + 2;
                        const char *src = op + 1;
                        const char *bn = strrchr(src, '/');
                        bn = bn ? bn + 1 : src;
                        char full[PATH_MAX];
                        snprintf(full, sizeof full, "%s/%s", vdir, src);
                        if (access(full, R_OK) != 0)
                            fprintf(stderr, "avviso: immagine %s non trovata\n", full);
                        else
                            chap_add_seg(c, SEG_IMAGE, bn, NULL, cap[0] ? cap : NULL);
                    }
                } else {
                text_para: ;
                    size_t L = strlen(t);
                    if (plen + L + 2 > plcap) {
                        plcap = plen + L + 64;
                        para = realloc(para, plcap);
                    }
                    if (plen) { para[plen++] = '\n'; }
                    memcpy(para + plen, t, L);
                    plen += L; para[plen] = 0;
                }
            }
            if (!has_next) break;
            lp = e + 1;
        }
        FLUSH_PARA();
#undef FLUSH_PARA
        free(para);
        free(md);
        free(fmid);
        free(fmtitle);
        free(fmtype);
    }
    for (size_t i = 0; i < nen; i++) { free(ents[i].file); free(ents[i].id); free(ents[i].title); free(ents[i].type); }
    free(ents);
    free(xy);
    if (!bk->nch) { fprintf(stderr, "errore: nessun capitolo nel vault\n"); return -1; }
    return 0;
}

static int cmd_info_vault(const char *vdir) {
    Book bk;
    if (load_vault(vdir, &bk) != 0) return 1;
    printf("titolo:     %s\n", bk.title ? bk.title : "");
    printf("autore:     %s\n", bk.author ? bk.author : "");
    printf("lingua:     %s\n", bk.language ? bk.language : "");
    if (bk.bookuuid) printf("uuid:       %s\n", bk.bookuuid);
    if (bk.style) printf("stile:      %s\n", bk.style);
    if (bk.opening) printf("apertura:   %s\n", bk.opening);
    if (bk.font) printf("font:       %s\n", bk.font);
    if (bk.scenebreak) printf("scenebreak: %s\n", bk.scenebreak);
    if (bk.cover) printf("cover:      %s\n", bk.cover);
    if (bk.trim) printf("trim:       %s\n", bk.trim);
    if (bk.pages > 0) printf("pages:      %d\n", bk.pages);
    if (bk.bleed) printf("bleed:      %s\n", bk.bleed ? "true" : "false");
    printf("capitoli:   %zu\n", bk.nch);
    for (size_t i = 0; i < bk.nch; i++) {
        printf("  %zu. file=%s | id=%s | title=%s | type=%s\n",
               i + 1,
               bk.chs[i].file ? bk.chs[i].file : "",
               bk.chs[i].id ? bk.chs[i].id : "",
               bk.chs[i].title ? bk.chs[i].title : "",
               bk.chs[i].type ? bk.chs[i].type : "");
    }
    return 0;
}

static int cmd_init(const char *vdir, const char *title) {
    char tdir[PATH_MAX];
    snprintf(tdir, sizeof tdir, "%s", vdir);
    mkdir_p(tdir);

    char cov[PATH_MAX], img[PATH_MAX];
    snprintf(cov, sizeof cov, "%s/cover", tdir);
    snprintf(img, sizeof img, "%s/images", tdir);
    mkdir_p(cov);
    mkdir_p(img);

    const char *btitle = (title && title[0]) ? title : "Nuovo Libro";

    char yml[PATH_MAX];
    snprintf(yml, sizeof yml, "%s/book.yaml", tdir);
    if (access(yml, F_OK) == 0) {
        fprintf(stderr, "errore: %s esiste gia'\n", yml);
        return 1;
    }
    FILE *fy = fopen(yml, "w");
    if (!fy) { perror("fopen book.yaml"); return 1; }
    fprintf(fy, "# PubCraft vault\n"
                "title: %s\n"
                "author: Autore\n"
                "language: it\n"
                "style: classic\n"
                "opening: dropcap\n"
                "chapters:\n"
                "- file: 01-capitolo-1.md\n"
                "  title: Capitolo 1\n", btitle);
    fclose(fy);

    char ch1[PATH_MAX];
    snprintf(ch1, sizeof ch1, "%s/01-capitolo-1.md", tdir);
    FILE *fc = fopen(ch1, "w");
    if (fc) {
        fprintf(fc, "---\ntitle: Capitolo 1\n---\n\nInizia a scrivere qui il tuo libro...\n");
        fclose(fc);
    }
    printf("Nuovo progetto PubCraft creato in %s/\n", tdir);
    return 0;
}

static void usage(const char *prog) {
    fprintf(stderr,
        "pubcraft %s - Markdown Book Formatter & Publisher (stampa KDP + EPUB)\n"
        "uso:\n"
        "  %s init <vaultdir> [titolo]                         (crea un nuovo progetto libro da zero)\n"
        "  %s build-print <vaultdir> <outdir> [opzioni stampa] (compila vault Markdown -> PDF KDP)\n"
        "  %s build <vaultdir> <out.epub> [--kindle]           (compila vault Markdown -> EPUB)\n"
        "  %s export-md <archivio> <vaultdir>                  (importa archivio -> vault Markdown)\n"
        "  %s info <archivio> | list | open                    (ispezione archivio)\n"
        "  %s export-html <archivio> <outdir>\n"
        "  %s export-epub <archivio> <out.epub> [--kindle]\n"
        "  %s export-print <archivio> <outdir> [opzioni stampa]\n"
        "opzioni stampa: [--trim 6x9] [--pages N] [--bleed|--no-bleed] [--pdf]\n"
        "trim KDP: 5x8 5.25x8 5.5x8.5 6x9 7x10 8x10 8.5x8.5 8.5x11\n",
        VERSION, prog, prog, prog, prog, prog, prog, prog, prog);
}

static int parse_print_opts(int argc, char **argv, int start,
                            double *tw, double *th, int *pages, int *bleed, int *want_pdf) {
    for (int i = start; i < argc; i++) {
        const char *val = NULL;
        char opt[32];
        const char *eq = strchr(argv[i], '=');
        if (eq && (size_t)(eq - argv[i]) < sizeof opt) {
            memcpy(opt, argv[i], (size_t)(eq - argv[i]));
            opt[eq - argv[i]] = 0;
            val = eq + 1;
        } else {
            snprintf(opt, sizeof opt, "%s", argv[i]);
            if ((strcmp(opt, "--trim") == 0 || strcmp(opt, "--pages") == 0) && i + 1 < argc)
                val = argv[++i];
        }
        if (!strcmp(opt, "--trim")) {
            double w = 0, h = 0;
            if (val && sscanf(val, "%lfx%lf", &w, &h) == 2 && w > 0 && h > 0) {
                *tw = w; *th = h;
            } else { fprintf(stderr, "trim non valido (es. --trim 6x9)\n"); return -1; }
        } else if (!strcmp(opt, "--pages")) {
            *pages = val ? atoi(val) : 0;
            if (*pages < 24) *pages = 24;
        } else if (!strcmp(argv[i], "--bleed")) *bleed = 1;
        else if (!strcmp(argv[i], "--no-bleed")) *bleed = 0;
        else if (!strcmp(argv[i], "--pdf")) *want_pdf = 1;
        else { fprintf(stderr, "opzione sconosciuta: %s\n", argv[i]); return -1; }
    }
    int known = 0;
    for (const struct Trim *t = TRIMS; t->name; t++)
        if (t->w == *tw && t->h == *th) known = 1;
    if (!known)
        fprintf(stderr, "avviso: %.2fx%.2f non e' un trim KDP standard, lo uso comunque\n", *tw, *th);
    return 0;
}

/* build da vault: carica Markdown, copia images/, esporta */
static int build_epub(const char *vdir, const char *outepub, int kindle) {
    Book bk;
    if (load_vault(vdir, &bk) != 0) return 1;
    char imgd[PATH_MAX];
    pc_tmp_dir(imgd, sizeof imgd, "pubcraft-vi");
    copy_vault_images(vdir, imgd);
    /* la cartella genitore dell'output deve esistere (zip non la crea) */
    char parent[PATH_MAX];
    snprintf(parent, sizeof parent, "%s", outepub);
    char *slash = strrchr(parent, '/');
    if (!slash) slash = strrchr(parent, '\\');
    if (slash) { *slash = 0; mkdir_p(parent); }
    int rc = export_epub_book(&bk, imgd, outepub, kindle);
    rm_rf_path(imgd);
    return rc;
}
static int build_print(const char *vdir, const char *outdir,
                       double tw, double th, int pages, int bleed, int want_pdf) {
    Book bk;
    if (load_vault(vdir, &bk) != 0) return 1;
    char imgd[PATH_MAX];
    snprintf(imgd, sizeof imgd, "%s/images", outdir);
    copy_vault_images(vdir, imgd);
    char fnd[PATH_MAX];
    snprintf(fnd, sizeof fnd, "%s/fonts", outdir);
    copy_fonts(vdir, fnd);
    return export_print_book(&bk, imgd, outdir, tw, th, pages, bleed, want_pdf);
}

int main(int argc, char **argv) {
    locate_executable();
    if (argc < 3) { usage(argv[0]); return 2; }
    /* Se l'argomento è un vault (book.yaml presente), i comandi da archivio compresso
     * danno un errore confuso ("unzip fallito"): indico subito il comando corretto. */
    {
        int wants_archive = !strcmp(argv[1], "list") ||
            !strcmp(argv[1], "open") || !strcmp(argv[1], "export-html") ||
            !strcmp(argv[1], "export-epub") || !strcmp(argv[1], "export-kindle") ||
            !strcmp(argv[1], "export-md");
        if (wants_archive) {
            char yml[PATH_MAX];
            snprintf(yml, sizeof yml, "%s/book.yaml", argv[2]);
            FILE *t = fopen(yml, "r");
            if (t) {
                fclose(t);
                fprintf(stderr, "errore: '%s' è un vault markdown (ha book.yaml), non un file compresso di progetto.\n"
                        "       Usa 'pubcraft build <vault> <out.epub>' o 'pubcraft build-print <vault> <dir>'.\n",
                        argv[2]);
                return 1;
            }
        }
    }
    if (!strcmp(argv[1], "init") && (argc == 3 || argc == 4))
        return cmd_init(argv[2], argc == 4 ? argv[3] : NULL);
    if (!strcmp(argv[1], "info") && argc == 3) {
        char yml[PATH_MAX];
        snprintf(yml, sizeof yml, "%s/book.yaml", argv[2]);
        FILE *t = fopen(yml, "r");
        if (t) {
            fclose(t);
            return cmd_info_vault(argv[2]);
        }
        return cmd_info(argv[2]);
    }
    if (!strcmp(argv[1], "list") && argc == 3) return cmd_list(argv[2]);
    if (!strcmp(argv[1], "open") && argc == 3) return cmd_open(argv[2]);
    if (!strcmp(argv[1], "export-html") && argc == 4) return export_html(argv[2], argv[3]);
    if (!strcmp(argv[1], "export-epub") && argc == 4) return export_epub_impl(argv[2], argv[3], 0);
    if (!strcmp(argv[1], "export-kindle") && argc == 4) return export_epub_impl(argv[2], argv[3], 1);
    if (!strcmp(argv[1], "export-print") && argc >= 4) {
        double tw = 6, th = 9;
        int pages = 200, bleed = 0, want_pdf = 0;
        if (parse_print_opts(argc, argv, 4, &tw, &th, &pages, &bleed, &want_pdf) != 0) return 2;
        return export_print(argv[2], argv[3], tw, th, pages, bleed, want_pdf);
    }
    if (!strcmp(argv[1], "export-md") && argc == 4) return export_md(argv[2], argv[3]);
    if (!strcmp(argv[1], "build") && argc >= 4) {
        int kindle = 0;
        for (int i = 4; i < argc; i++) {
            if (!strcmp(argv[i], "--kindle")) kindle = 1;
            else { fprintf(stderr, "opzione sconosciuta: %s\n", argv[i]); return 2; }
        }
        return build_epub(argv[2], argv[3], kindle);
    }
    if (!strcmp(argv[1], "build-print") && argc >= 4) {
        double tw = 6, th = 9;
        int pages = 200, bleed = 0, want_pdf = 0;
        if (parse_print_opts(argc, argv, 4, &tw, &th, &pages, &bleed, &want_pdf) != 0) return 2;
        return build_print(argv[2], argv[3], tw, th, pages, bleed, want_pdf);
    }
    usage(argv[0]);
    return 2;
}
