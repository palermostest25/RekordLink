#import <Cocoa/Cocoa.h>

@interface RekordLinkDelegate : NSObject <NSApplicationDelegate>
@property(nonatomic, strong) NSStatusItem *statusItem;
@property(nonatomic, strong) NSMenuItem *stateItem;
@property(nonatomic, strong) NSMenuItem *startItem;
@property(nonatomic, strong) NSMenuItem *stopItem;
@property(nonatomic, strong) NSMenuItem *restartItem;
@property(nonatomic, strong) NSTask *uiTask;
@property(nonatomic, strong) NSPipe *uiControlPipe;
@end

@implementation RekordLinkDelegate

- (NSString *)rekordLinkPath {
    return [[[NSBundle mainBundle] resourcePath] stringByAppendingPathComponent:@"rekordlink"];
}

- (void)applicationDidFinishLaunching:(NSNotification *)notification {
    (void)notification;
    [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];

    self.statusItem = [[NSStatusBar systemStatusBar] statusItemWithLength:NSSquareStatusItemLength];
    NSStatusBarButton *button = self.statusItem.button;
    NSImage *image = [NSImage imageWithSystemSymbolName:@"arrow.triangle.2.circlepath"
                              accessibilityDescription:@"RekordLink"];
    if (image != nil) {
        image.template = YES;
        button.image = image;
        button.imagePosition = NSImageOnly;
        button.title = @"";
    } else {
        button.title = @"RL";
    }
    button.toolTip = @"RekordLink duo library sync";

    NSMenu *menu = [[NSMenu alloc] init];
    self.stateItem = [[NSMenuItem alloc] initWithTitle:@"Sync: Checking…" action:nil keyEquivalent:@""];
    self.stateItem.enabled = NO;
    [menu addItem:self.stateItem];
    [menu addItem:[NSMenuItem separatorItem]];
    [menu addItemWithTitle:@"Open Dashboard" action:@selector(openDashboard:) keyEquivalent:@"o"].target = self;
    [menu addItem:[NSMenuItem separatorItem]];
    self.startItem = [menu addItemWithTitle:@"Start Background Sync" action:@selector(startSync:) keyEquivalent:@""];
    self.startItem.target = self;
    self.stopItem = [menu addItemWithTitle:@"Stop Background Sync" action:@selector(stopSync:) keyEquivalent:@""];
    self.stopItem.target = self;
    self.restartItem = [menu addItemWithTitle:@"Restart Background Sync" action:@selector(restartSync:) keyEquivalent:@""];
    self.restartItem.target = self;
    [menu addItem:[NSMenuItem separatorItem]];
    NSMenuItem *quit = [menu addItemWithTitle:@"Quit RekordLink UI" action:@selector(quit:) keyEquivalent:@"q"];
    quit.target = self;
    self.statusItem.menu = menu;

    [self startUIServer];
    [self refreshStatus];
    [NSTimer scheduledTimerWithTimeInterval:5.0 target:self selector:@selector(refreshStatusTimer:) userInfo:nil repeats:YES];
}

- (void)startUIServer {
    NSString *cli = [self rekordLinkPath];
    if (![[NSFileManager defaultManager] isExecutableFileAtPath:cli]) {
        self.stateItem.title = @"UI: bundled engine is missing";
        return;
    }
    NSTask *task = [[NSTask alloc] init];
    task.executableURL = [NSURL fileURLWithPath:cli];
    task.arguments = @[@"ui", @"--listen", @"127.0.0.1:9766", @"--no-browser", @"--exit-on-stdin-close"];
    self.uiControlPipe = [NSPipe pipe];
    task.standardInput = self.uiControlPipe;
    task.standardOutput = [NSFileHandle fileHandleWithNullDevice];
    task.standardError = [NSFileHandle fileHandleWithNullDevice];
    NSError *error = nil;
    if ([task launchAndReturnError:&error]) {
        self.uiTask = task;
    } else {
        self.stateItem.title = [NSString stringWithFormat:@"UI: %@", error.localizedDescription];
    }
}

- (void)openDashboard:(id)sender {
    (void)sender;
    [[NSWorkspace sharedWorkspace] openURL:[NSURL URLWithString:@"http://127.0.0.1:9766"]];
}

- (void)runServiceAction:(NSString *)action {
    NSTask *task = [[NSTask alloc] init];
    task.executableURL = [NSURL fileURLWithPath:[self rekordLinkPath]];
    task.arguments = @[@"service", action];
    task.standardOutput = [NSFileHandle fileHandleWithNullDevice];
    task.standardError = [NSFileHandle fileHandleWithNullDevice];
    __weak RekordLinkDelegate *weakSelf = self;
    task.terminationHandler = ^(NSTask *completed) {
        (void)completed;
        dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(0.5 * NSEC_PER_SEC)), dispatch_get_main_queue(), ^{
            [weakSelf refreshStatus];
        });
    };
    NSError *error = nil;
    if (![task launchAndReturnError:&error]) {
        self.stateItem.title = [NSString stringWithFormat:@"Action failed: %@", error.localizedDescription];
    }
}

- (void)startSync:(id)sender { (void)sender; [self runServiceAction:@"start"]; }
- (void)stopSync:(id)sender { (void)sender; [self runServiceAction:@"stop"]; }
- (void)restartSync:(id)sender { (void)sender; [self runServiceAction:@"restart"]; }

- (void)refreshStatusTimer:(NSTimer *)timer { (void)timer; [self refreshStatus]; }

- (void)refreshStatus {
    NSTask *task = [[NSTask alloc] init];
    NSPipe *pipe = [NSPipe pipe];
    task.executableURL = [NSURL fileURLWithPath:[self rekordLinkPath]];
    task.arguments = @[@"service", @"status"];
    task.standardOutput = pipe;
    task.standardError = [NSFileHandle fileHandleWithNullDevice];
    __weak RekordLinkDelegate *weakSelf = self;
    task.terminationHandler = ^(NSTask *completed) {
        NSData *data = [[pipe fileHandleForReading] readDataToEndOfFile];
        NSString *output = [[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding];
        dispatch_async(dispatch_get_main_queue(), ^{
            RekordLinkDelegate *strongSelf = weakSelf;
            if (strongSelf == nil) return;
            BOOL configured = completed.terminationStatus == 0;
            BOOL running = configured && [output containsString:@"state: running"];
            strongSelf.stateItem.title = running ? @"Sync: Running" : (configured ? @"Sync: Stopped" : @"Sync: Not configured");
            strongSelf.startItem.enabled = configured && !running;
            strongSelf.stopItem.enabled = running;
            strongSelf.restartItem.enabled = configured;
        });
    };
    NSError *error = nil;
    if (![task launchAndReturnError:&error]) {
        self.stateItem.title = @"Sync: Status unavailable";
    }
}

- (void)quit:(id)sender {
    (void)sender;
    [NSApp terminate:nil];
}

- (void)applicationWillTerminate:(NSNotification *)notification {
    (void)notification;
    if (self.uiTask.running) {
        [[self.uiControlPipe fileHandleForWriting] closeFile];
        [self.uiTask terminate];
    }
}

@end

int main(int argc, const char *argv[]) {
    (void)argc;
    (void)argv;
    @autoreleasepool {
        NSApplication *app = [NSApplication sharedApplication];
        RekordLinkDelegate *delegate = [[RekordLinkDelegate alloc] init];
        app.delegate = delegate;
        [app run];
    }
    return 0;
}
