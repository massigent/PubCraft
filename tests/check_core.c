#define main original_main
#include "../src/pubcraft.c"
#undef main

int main(int argc, char **argv) {
    if (argc < 2) return 2;
    printf("pages=%d\n", count_pdf_pages(argv[1]));
    printf("status42=%d status1=%d\n", run_cmd_status("exit 42"), run_cmd_status("exit 1"));
    return 0;
}
