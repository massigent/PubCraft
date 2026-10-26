// PubCraft native Universal 2 app launcher. MIT — PubCraft Contributors.
// Do not replace this with a system prebuilt AppleScript applet: its ARM slice
// can silently require a newer macOS than our documented deployment target.
#import <Cocoa/Cocoa.h>

@interface PCLauncher : NSObject <NSApplicationDelegate>
@property(nonatomic) BOOL launched;
@property(nonatomic) int exitStatus;
@end

@implementation PCLauncher
- (void)launchGUIWithPath:(NSString *)path {
    if (self.launched) return;
    self.launched = YES;
    NSURL *directory = [NSBundle.mainBundle.executableURL URLByDeletingLastPathComponent];
    NSTask *task = [NSTask new];
    task.executableURL = [directory URLByAppendingPathComponent:@"pubcraftgui"];
    task.arguments = path ? @[path] : @[];
    task.standardOutput = NSFileHandle.fileHandleWithNullDevice;
    task.standardError = NSFileHandle.fileHandleWithNullDevice;
    NSError *error = nil;
    if (![task launchAndReturnError:&error]) {
        self.exitStatus = 1;
        fprintf(stderr, "PubCraft launch failed: %s\n", error.localizedDescription.UTF8String);
        NSAlert *alert = [NSAlert new];
        alert.messageText = @"PubCraft could not start";
        alert.informativeText = error.localizedDescription;
        [alert addButtonWithTitle:@"OK"];
        [alert runModal];
    }
    // Let an open-files handler acknowledge its event before terminating.
    dispatch_async(dispatch_get_main_queue(), ^{ [NSApp terminate:nil]; });
}
- (void)applicationDidFinishLaunching:(NSNotification *)notification {
    (void)notification;
    [self launchGUIWithPath:nil];
}
- (void)application:(NSApplication *)application openFiles:(NSArray<NSString *> *)filenames {
    [self launchGUIWithPath:filenames.firstObject];
    [application replyToOpenOrPrint:NSApplicationDelegateReplySuccess];
}
@end

int main(int argc, const char *argv[]) {
    (void)argc; (void)argv;
    @autoreleasepool {
        NSApplication *application = NSApplication.sharedApplication;
        [application setActivationPolicy:NSApplicationActivationPolicyAccessory];
        // NSApplication's delegate is weak; retain it for the entire event loop.
        static PCLauncher *delegate;
        delegate = [PCLauncher new];
        application.delegate = delegate;
        [application run];
        return delegate.exitStatus;
    }
}
