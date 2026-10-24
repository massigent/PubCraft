// pubcraft-html2pdf — Generatore nativo PDF da HTML per macOS basato su WebKit
// Licenza MIT — 2026 PubCraft Contributors

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

@interface PDFMaker : NSObject <WKNavigationDelegate>
@property (nonatomic, strong) WKWebView *webView;
@property (nonatomic, copy) NSString *outPath;
@property (nonatomic, assign) BOOL done;
@property (nonatomic, assign) int exitCode;
@end

@implementation PDFMaker
- (void)webView:(WKWebView *)webView didFinishNavigation:(WKNavigation *)navigation {
    // Breve pausa per completamento rendering font e layout
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(300 * NSEC_PER_MSEC)), dispatch_get_main_queue(), ^{
        WKPDFConfiguration *config = [[WKPDFConfiguration alloc] init];
        [self.webView createPDFWithConfiguration:config completionHandler:^(NSData *pdfData, NSError *error) {
            if (error) {
                fprintf(stderr, "Errore generazione PDF WebKit: %s\n", error.localizedDescription.UTF8String);
                self.exitCode = 1;
            } else if (!pdfData || pdfData.length == 0) {
                fprintf(stderr, "Errore: dati PDF vuoti\n");
                self.exitCode = 2;
            } else {
                BOOL ok = [pdfData writeToFile:self.outPath atomically:YES];
                if (ok) {
                    printf("PDF generato con successo: %s (%lu byte)\n",
                           self.outPath.UTF8String, (unsigned long)pdfData.length);
                    self.exitCode = 0;
                } else {
                    fprintf(stderr, "Errore scrittura file: %s\n", self.outPath.UTF8String);
                    self.exitCode = 3;
                }
            }
            self.done = YES;
        }];
    });
}

- (void)webView:(WKWebView *)webView didFailNavigation:(WKNavigation *)navigation withError:(NSError *)error {
    fprintf(stderr, "Errore caricamento HTML: %s\n", error.localizedDescription.UTF8String);
    self.exitCode = 4;
    self.done = YES;
}
@end

int main(int argc, char *argv[]) {
    @autoreleasepool {
        if (argc < 3) {
            fprintf(stderr, "Uso: pubcraft-html2pdf <input.html> <output.pdf>\n"
                            "Nota: pubcraft-html2pdf genera una cattura WebKit continua a pagina singola.\n"
                            "Per l'impaginazione KDP di stampa multi-pagina, usa un motore paginato (Chromium/Chrome/Brave o WeasyPrint).\n");
            return 1;
        }
        fprintf(stderr, "pubcraft-html2pdf: avviso — generazione snapshot continuo WebKit (non paginato KDP).\n");
        [NSApplication sharedApplication];
        NSString *inPath = [NSString stringWithUTF8String:argv[1]].stringByExpandingTildeInPath;
        NSString *outPath = [NSString stringWithUTF8String:argv[2]].stringByExpandingTildeInPath;
        NSURL *fileURL = [NSURL fileURLWithPath:inPath];
        NSURL *readDir = [fileURL URLByDeletingLastPathComponent];

        WKWebViewConfiguration *config = [[WKWebViewConfiguration alloc] init];
        WKWebView *wv = [[WKWebView alloc] initWithFrame:NSMakeRect(0, 0, 1200, 1600) configuration:config];

        PDFMaker *maker = [[PDFMaker alloc] init];
        maker.webView = wv;
        maker.outPath = outPath;
        wv.navigationDelegate = maker;

        [wv loadFileURL:fileURL allowingReadAccessToURL:readDir];

        NSDate *timeout = [NSDate dateWithTimeIntervalSinceNow:60];
        while (!maker.done && [[NSRunLoop currentRunLoop] runMode:NSDefaultRunLoopMode beforeDate:[NSDate dateWithTimeIntervalSinceNow:0.1]]) {
            if ([[NSDate date] compare:timeout] == NSOrderedDescending) {
                fprintf(stderr, "Timeout durante la generazione del PDF\n");
                return 5;
            }
        }
        return maker.exitCode;
    }
}
