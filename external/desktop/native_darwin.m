//go:build desktop && desktop.native && darwin && cgo

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#import <CoreGraphics/CGSession.h>
#include "native_darwin.h"

@interface CoddyNativeWindow : NSObject <NSWindowDelegate, NSApplicationDelegate, WKUIDelegate>
@property(strong) NSWindow *window;
@property(strong) WKWebView *webview;
@property BOOL closed;
@end

@implementation CoddyNativeWindow
- (void)windowWillClose:(NSNotification *)notification {
    self.closed = YES;
    [NSApp stop:nil];
    // stop: takes effect after the current event; wake a loop idle in nextEvent.
    [NSApp postEvent:[NSEvent otherEventWithType:NSEventTypeApplicationDefined
        location:NSZeroPoint modifierFlags:0 timestamp:0 windowNumber:0
        context:nil subtype:0 data1:0 data2:0] atStart:NO];
}
- (void)quit:(id)sender { [self.window close]; }
- (void)applicationWillFinishLaunching:(NSNotification *)notification {
    // Replace AppKit's quit handler after it installs the standard handlers.
    // AppKit's deferred termination enters a nested loop; the Go caller must
    // instead regain control to stop its HTTP surface and exit normally.
    [[NSAppleEventManager sharedAppleEventManager] setEventHandler:self
        andSelector:@selector(handleQuitEvent:withReplyEvent:)
        forEventClass:kCoreEventClass andEventID:kAEQuitApplication];
}
- (void)handleQuitEvent:(NSAppleEventDescriptor *)event withReplyEvent:(NSAppleEventDescriptor *)reply {
    // Returning normally acknowledges the system request without a veto.
    [self quit:nil];
}
- (void)webView:(WKWebView *)webView runOpenPanelWithParameters:(WKOpenPanelParameters *)parameters
    initiatedByFrame:(WKFrameInfo *)frame completionHandler:(void (^)(NSArray<NSURL *> *))completionHandler {
    NSOpenPanel *panel = [NSOpenPanel openPanel];
    panel.allowsMultipleSelection = parameters.allowsMultipleSelection;
    panel.canChooseDirectories = parameters.allowsDirectories;
    panel.canChooseFiles = YES;
    [panel beginSheetModalForWindow:self.window completionHandler:^(NSModalResponse response) {
        completionHandler(response == NSModalResponseOK ? panel.URLs : nil);
    }];
}
- (WKWebView *)webView:(WKWebView *)webView createWebViewWithConfiguration:(WKWebViewConfiguration *)configuration
    forNavigationAction:(WKNavigationAction *)action windowFeatures:(WKWindowFeatures *)features {
    // Match shell.ExternalURL: only bounded, absolute HTTP(S) links leave
    // Coddy. In particular, a page cannot invoke file: or an app's URL scheme.
    NSURL *url = action.request.URL;
    NSString *raw = url.absoluteString;
    NSString *scheme = url.scheme.lowercaseString;
    if (!action.targetFrame && [raw lengthOfBytesUsingEncoding:NSUTF8StringEncoding] <= 4096 && url.host.length &&
        ([scheme isEqualToString:@"https"] || [scheme isEqualToString:@"http"])) {
        [[NSWorkspace sharedWorkspace] openURL:url];
    }
    return nil;
}
@end

int coddy_native_main_thread(void) {
    @autoreleasepool { return [NSThread isMainThread]; }
}

int coddy_native_available(void) {
    @autoreleasepool {
        // Do this before NSApplication or any window: a headless launch must be
        // able to fall back to the browser backend without Cocoa terminating it.
        if (@available(macOS 14.0, *)) {} else { return 0; }
        CFDictionaryRef session = CGSessionCopyCurrentDictionary();
        if (!session) return 0;
        CFTypeRef onConsole = CFDictionaryGetValue(session, kCGSessionOnConsoleKey);
        BOOL available = onConsole && CFEqual(onConsole, kCFBooleanTrue);
        CFRelease(session);
        return available;
    }
}

static void coddyDecorate(NSWindow *window, BOOL framed) {
    // Keep NSWindowStyleMaskTitled even without chrome: a borderless window
    // cannot become the key window, leaving WKWebView unable to accept input.
    window.styleMask = NSWindowStyleMaskTitled | NSWindowStyleMaskClosable |
        NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable |
        (framed ? 0 : NSWindowStyleMaskFullSizeContentView);
    window.titleVisibility = framed ? NSWindowTitleVisible : NSWindowTitleHidden;
    window.titlebarAppearsTransparent = !framed;
    for (NSNumber *button in @[@(NSWindowCloseButton), @(NSWindowMiniaturizeButton), @(NSWindowZoomButton)]) {
        [window standardWindowButton:button.integerValue].hidden = !framed;
    }
}

static void coddyMenu(CoddyNativeWindow *controller) {
    NSMenu *menu = [[NSMenu alloc] init];
    NSMenuItem *app = [[NSMenuItem alloc] init];
    [menu addItem:app];
    app.submenu = [[NSMenu alloc] initWithTitle:@"Coddy"];
    NSMenuItem *quit = [app.submenu addItemWithTitle:@"Quit Coddy" action:@selector(quit:) keyEquivalent:@"q"];
    quit.target = controller;
    NSMenuItem *edit = [[NSMenuItem alloc] init];
    [menu addItem:edit];
    edit.submenu = [[NSMenu alloc] initWithTitle:@"Edit"];
    [edit.submenu addItemWithTitle:@"Undo" action:@selector(undo:) keyEquivalent:@"z"];
    [edit.submenu addItemWithTitle:@"Cut" action:@selector(cut:) keyEquivalent:@"x"];
    [edit.submenu addItemWithTitle:@"Copy" action:@selector(copy:) keyEquivalent:@"c"];
    [edit.submenu addItemWithTitle:@"Paste" action:@selector(paste:) keyEquivalent:@"v"];
    [edit.submenu addItemWithTitle:@"Select All" action:@selector(selectAll:) keyEquivalent:@"a"];
    // T/W/R and Ctrl+Tab stay with shell.js; WebKit has no browser tab menu
    // to consume them, and the shell already interprets Cmd on macOS.
    NSApp.mainMenu = menu;
}

void *coddy_native_create(const char *url, const char *store, int framed, int minimised, int min_width, int min_height) {
    @autoreleasepool {
        [NSApplication sharedApplication];
        [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
        CoddyNativeWindow *controller = [[CoddyNativeWindow alloc] init];
        NSWindow *window = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 1280, 800)
            styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable |
                NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable
            backing:NSBackingStoreBuffered defer:NO];
        controller.window = window;
        window.releasedWhenClosed = NO;
        window.title = @"Coddy";
        window.minSize = NSMakeSize(min_width, min_height);
        window.delegate = controller;
        coddyDecorate(window, framed);
        WKWebViewConfiguration *configuration = [[WKWebViewConfiguration alloc] init];
        if (@available(macOS 14.0, *)) {
            NSUUID *identifier = [[NSUUID alloc] initWithUUIDString:[NSString stringWithUTF8String:store]];
            if (!identifier) { [window close]; return NULL; }
            configuration.websiteDataStore = [WKWebsiteDataStore dataStoreForIdentifier:identifier];
        }
        WKWebView *webview = [[WKWebView alloc] initWithFrame:window.contentView.bounds configuration:configuration];
        webview.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
        webview.UIDelegate = controller;
        controller.webview = webview;
        window.contentView = webview;
        NSApp.delegate = controller;
        coddyMenu(controller);
        [window center];
        [window makeKeyAndOrderFront:nil];
        [NSApp activateIgnoringOtherApps:YES];
        [webview loadRequest:[NSURLRequest requestWithURL:[NSURL URLWithString:[NSString stringWithUTF8String:url]]]];
        if (minimised) [window miniaturize:nil];
        return (__bridge_retained void *)controller;
    }
}

void coddy_native_run(void *opaque) {
    @autoreleasepool {
        CoddyNativeWindow *controller = (__bridge CoddyNativeWindow *)opaque;
        if (!controller.closed) [NSApp run];
        controller.closed = YES;
    }
}

void coddy_native_release(void *opaque) {
    @autoreleasepool {
        CoddyNativeWindow *controller = (__bridge_transfer CoddyNativeWindow *)opaque;
        [[NSAppleEventManager sharedAppleEventManager] removeEventHandlerForEventClass:kCoreEventClass
            andEventID:kAEQuitApplication];
        [controller.webview stopLoading];
        controller.webview.UIDelegate = nil;
        controller.window.contentView = nil;
        controller.webview = nil;
        controller.window.delegate = nil;
        if (NSApp.delegate == controller) NSApp.delegate = nil;
        NSApp.mainMenu = nil;
    }
}

// The Go callback shares drag arithmetic with X11. Cocoa only reads and sets
// frames, translating its bottom-left origin to top-left virtual-desktop points.
static CGFloat coddyDesktopTop(void) {
    // One origin for every display keeps gesture coordinates stable across
    // displays and gives the shared Y >= 0 clamp the whole visible desktop.
    CGFloat top = 0;
    for (NSScreen *screen in NSScreen.screens) top = MAX(top, NSMaxY(screen.visibleFrame));
    return top;
}

CoddyFrame coddy_native_frame(void *opaque) {
    @autoreleasepool {
        NSWindow *window = ((__bridge CoddyNativeWindow *)opaque).window;
        NSRect frame = window.frame;
        CGFloat top = coddyDesktopTop();
        return (CoddyFrame){(int)frame.origin.x, (int)(top - NSMaxY(frame)),
            (int)frame.size.width, (int)frame.size.height};
    }
}
void coddy_native_set_frame(void *opaque, CoddyFrame frame) {
    @autoreleasepool {
        NSWindow *window = ((__bridge CoddyNativeWindow *)opaque).window;
        CGFloat top = coddyDesktopTop();
        [window setFrame:NSMakeRect(frame.x, top - frame.y - frame.height, frame.width, frame.height) display:YES];
    }
}
int coddy_native_zoomed(void *opaque) {
    @autoreleasepool { return ((__bridge CoddyNativeWindow *)opaque).window.zoomed; }
}
void coddy_native_unzoom(void *opaque) {
    @autoreleasepool { [((__bridge CoddyNativeWindow *)opaque).window zoom:nil]; }
}

extern void coddy_native_geometry(uintptr_t callback, void *window, int action, int x, int y);
void coddy_native_action(void *opaque, uintptr_t callback, int action, int x, int y) {
    @autoreleasepool {
        CoddyNativeWindow *controller = (__bridge CoddyNativeWindow *)opaque;
        dispatch_async(dispatch_get_main_queue(), ^{
            @autoreleasepool {
                if (controller.closed) return;
                NSWindow *window = controller.window;
                switch (action) {
                case CoddyUndecorate: coddyDecorate(window, NO); break;
                case CoddyDecorate: coddyDecorate(window, YES); break;
                case CoddyMinimise: [window miniaturize:nil]; break;
                case CoddyMaximise: [window zoom:nil]; break;
                case CoddyClose: [window close]; break;
                case CoddyActivate:
                    [window deminiaturize:nil];
                    [window makeKeyAndOrderFront:nil];
                    [NSApp activateIgnoringOtherApps:YES];
                    break;
                default: coddy_native_geometry(callback, (__bridge void *)controller, action, x, y); break;
                }
            }
        });
    }
}
