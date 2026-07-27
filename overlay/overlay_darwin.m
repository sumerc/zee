#import <Cocoa/Cocoa.h>
#import <QuartzCore/QuartzCore.h>

// A Dynamic Island-style panel hanging off the top of the main display: a
// black notch shape, a status dot + label, and a scrolling level meter.
//
// Geometry is Textream's overlay, ported from SwiftUI to Core Animation: 16pt
// top inset with corners bowing downward into the menu bar, 18pt bottom
// radius, 3pt bars at 2pt spacing scaled to 28pt full deflection. The two
// phase reveal is the same too — the shape expands out of a collapsed notch
// first, contents fade in once it has room.
//
// Everything here must touch AppKit on the main thread; the zeeOverlay*
// entry points are the only ones Go calls and each hops onto the main queue.

#define NBARS      30
#define BAR_W       3.0
#define BAR_GAP     2.0
#define BAR_MAX_H  28.0
#define BAR_MIN_H   3.0
#define INSET      16.0   // horizontal inset of the shape's body
#define BOTTOM_R   18.0
#define PANEL_W   200.0
#define COLLAPSED_W 148.0 // width of the notch we grow out of
#define DOT         7.0
#define FONT_SIZE  11.0

// Row centres, measured down from where the overlay's own content starts (see
// gTopBase). The dot rides in the notch strip itself, label and meter hang
// below it.
// Reveal timing. Textream waits for the shape to finish expanding before
// fading its contents in, which suits a panel full of text; ours says one word
// and has to be readable the instant you press the key, so the fade starts
// while the shape is still opening.
#define GROW_DUR   0.22
#define FADE_AFTER 0.10
#define FADE_DUR   0.16

#define ROW_DOT    11.0
#define ROW_LABEL  28.0
#define ROW_BARS   58.0
#define BODY_H     82.0   // ROW_BARS + half a bar + bottom padding
#define COMPACT_H  46.0   // shape height with the meter hidden: label + padding

static NSPanel      *gPanel;
static CAShapeLayer *gShape;
static CALayer      *gContent; // dot + label + meter, faded as one on show/hide
static CALayer      *gMeter;   // just the bars, faded on its own per state
static CALayer      *gDot;
static CATextLayer  *gLabel;
static CALayer      *gBars[NBARS];
static float         gLevels[NBARS];
static CGColorRef    gAccent;
#define RAMP_STEPS 24
static CGColorRef    gRamp[RAMP_STEPS];
static CGFloat       gNotchH;  // height of the strip the shape grows out of
static CGFloat       gTopBase; // where our own content may start, from the top
static CGFloat       gPanelH;
static BOOL          gVisible;
static BOOL          gMeterShown = YES;

static void onMain(void (^block)(void)) {
	if ([NSThread isMainThread]) block();
	else dispatch_async(dispatch_get_main_queue(), block);
}

// notchPath is Textream's DynamicIslandShape in AppKit's bottom-left origin:
// the top corners bow *downward* so the shape reads as an extension of the
// menu bar, the bottom corners are ordinary convex rounds. The body spans
// [xOff, xOff+w] horizontally and rises h from the panel's bottom edge, which
// is what lets the reveal animate one path into another.
static CGPathRef notchPath(CGFloat xOff, CGFloat w, CGFloat h) {
	CGFloat t = INSET, br = BOTTOM_R;
	CGFloat l = xOff, r = xOff + w, top = gPanelH, bot = gPanelH - h;
	if (br > h / 2) br = h / 2;

	CGMutablePathRef p = CGPathCreateMutable();
	CGPathMoveToPoint(p, NULL, l, top);
	CGPathAddQuadCurveToPoint(p, NULL, l + t, top, l + t, top - t);
	CGPathAddLineToPoint(p, NULL, l + t, bot + br);
	CGPathAddQuadCurveToPoint(p, NULL, l + t, bot, l + t + br, bot);
	CGPathAddLineToPoint(p, NULL, r - t - br, bot);
	CGPathAddQuadCurveToPoint(p, NULL, r - t, bot, r - t, bot + br);
	CGPathAddLineToPoint(p, NULL, r - t, top - t);
	CGPathAddQuadCurveToPoint(p, NULL, r - t, top, r, top);
	CGPathCloseSubpath(p);
	return p;
}

static CGPathRef collapsedPath(void) {
	return notchPath((PANEL_W - COLLAPSED_W) / 2, COLLAPSED_W, gNotchH);
}

// shapeHeight is how far the panel hangs down: full when the meter is on show,
// cropped to the label when it is not.
static CGFloat shapeHeight(void) {
	return gMeterShown ? gPanelH : gTopBase + COMPACT_H;
}

static CGPathRef expandedPath(void) {
	return notchPath(0, PANEL_W, shapeHeight());
}

// rowY converts a ROW_* offset into AppKit's bottom-left origin.
static CGFloat rowY(CGFloat fromTop) { return gPanelH - gTopBase - fromTop; }

static NSFont *labelFont(void) {
	return [NSFont systemFontOfSize:FONT_SIZE weight:NSFontWeightSemibold];
}

// layoutStatus stacks the dot over the label, both centred on the panel, so
// the dot stays put as the text changes length.
static void layoutStatus(NSString *text) {
	CGSize sz = [text sizeWithAttributes:@{NSFontAttributeName: labelFont()}];
	gDot.frame = CGRectMake((PANEL_W - DOT) / 2, rowY(ROW_DOT) - DOT / 2, DOT, DOT);
	gLabel.frame = CGRectMake((PANEL_W - ceil(sz.width)) / 2 - 1, rowY(ROW_LABEL) - sz.height / 2,
	                          ceil(sz.width) + 2, ceil(sz.height));
	gLabel.string = text;
}

// layoutBars maps the level ring buffer onto the bars. Height is Textream's
// max(3, level*28); colour comes from the ramp, so a bar carries its level
// twice over — a loud one is both taller and hotter. Height alone is only a
// few points of difference at this size, and the eye reads brightness faster
// than it reads length.
static void layoutBars(void) {
	CGFloat startX = (PANEL_W - (NBARS * BAR_W + (NBARS - 1) * BAR_GAP)) / 2;
	CGFloat cy = rowY(ROW_BARS);
	for (int i = 0; i < NBARS; i++) {
		CGFloat lv = gLevels[i];
		CGFloat h = fmax(BAR_MIN_H, lv * BAR_MAX_H);
		int step = (int)(lv * (RAMP_STEPS - 1) + 0.5);
		if (step < 0) step = 0;
		if (step > RAMP_STEPS - 1) step = RAMP_STEPS - 1;
		gBars[i].frame = CGRectMake(startX + i * (BAR_W + BAR_GAP), cy - h / 2, BAR_W, h);
		gBars[i].backgroundColor = gRamp[step];
	}
}

// applyAccent rebuilds the level ramp: dim, deep accent at the bottom rising
// to a hot, near-white one at full deflection. Precomputed because layoutBars
// runs thirty times per frame and must not allocate.
static void applyAccent(void) {
	gDot.backgroundColor = gAccent;

	const CGFloat *c = CGColorGetComponents(gAccent);
	for (int i = 0; i < RAMP_STEPS; i++) {
		CGFloat t = (CGFloat)i / (RAMP_STEPS - 1);
		CGFloat dim = 0.40 + 0.60 * t;       // deepen the colour when quiet
		CGFloat hot = 0.55 * t * t;          // wash toward white when loud
		CGFloat rgb[3] = {c[0], c[1], c[2]};
		for (int k = 0; k < 3; k++) {
			CGFloat v = rgb[k] * dim;
			rgb[k] = v + (1.0 - v) * hot;
		}
		CGColorRelease(gRamp[i]);
		gRamp[i] = CGColorRetain([NSColor colorWithSRGBRed:rgb[0] green:rgb[1] blue:rgb[2] alpha:1.0].CGColor);
	}
}

// syncToScreen (re)computes everything that depends on which display is
// primary: the notch strip, the panel frame, and the layer tree sized to it.
// Runs on every show, not just at build — the screen list changes when
// monitors are plugged or unplugged, and a frame computed for the old primary
// display leaves the panel floating mid-screen on the new one.
static void syncToScreen(void) {
	NSScreen *screen = [NSScreen screens].firstObject;
	if (!screen) return;
	NSRect sf = screen.frame;
	CGFloat menuBarH = NSMaxY(sf) - NSMaxY(screen.visibleFrame);
	CGFloat safeTop = 0;
	if (@available(macOS 12.0, *)) safeTop = screen.safeAreaInsets.top;

	// On a plain display the status dot sits inside the menu bar strip, right
	// where Textream puts it. A hardware notch physically occupies that strip,
	// so there our content starts underneath it instead.
	gNotchH = safeTop > 0 ? safeTop : (menuBarH > 0 ? menuBarH : 24);
	gTopBase = safeTop;
	gPanelH = gTopBase + BODY_H;

	[gPanel setFrame:NSMakeRect(NSMidX(sf) - PANEL_W / 2, NSMaxY(sf) - gPanelH, PANEL_W, gPanelH)
	         display:NO];

	CGRect bounds = CGRectMake(0, 0, PANEL_W, gPanelH);
	[CATransaction begin];
	[CATransaction setDisableActions:YES];
	gPanel.contentView.frame = bounds;
	gPanel.contentView.layer.frame = bounds;
	gShape.frame = bounds;
	gContent.frame = bounds;
	gMeter.frame = bounds;
	gLabel.contentsScale = screen.backingScaleFactor;
	layoutStatus((NSString *)gLabel.string ?: @"Recording");
	layoutBars();
	[CATransaction commit];
}

static void buildPanel(void) {
	if (gPanel) return;

	gPanel = [[NSPanel alloc] initWithContentRect:NSZeroRect
	                                    styleMask:NSWindowStyleMaskBorderless | NSWindowStyleMaskNonactivatingPanel
	                                      backing:NSBackingStoreBuffered
	                                        defer:NO];
	gPanel.opaque = NO;
	gPanel.backgroundColor = [NSColor clearColor];
	gPanel.hasShadow = NO;
	gPanel.level = NSScreenSaverWindowLevel;
	// Fixed under the menu bar, and transparent to the mouse: it has nothing to
	// click and nowhere to go, so it must never take a click from the window
	// it happens to be covering.
	gPanel.ignoresMouseEvents = YES;
	gPanel.collectionBehavior = NSWindowCollectionBehaviorCanJoinAllSpaces |
	                            NSWindowCollectionBehaviorStationary |
	                            NSWindowCollectionBehaviorFullScreenAuxiliary |
	                            NSWindowCollectionBehaviorIgnoresCycle;

	CALayer *root = [CALayer layer];

	gShape = [CAShapeLayer layer];
	gShape.fillColor = [NSColor blackColor].CGColor;
	[root addSublayer:gShape];

	gContent = [CALayer layer];
	gContent.opacity = 0;
	[root addSublayer:gContent];

	gAccent = CGColorRetain([NSColor colorWithSRGBRed:1.0 green:0.27 blue:0.23 alpha:1.0].CGColor);

	gDot = [CALayer layer];
	gDot.cornerRadius = DOT / 2;
	[gContent addSublayer:gDot];

	gLabel = [CATextLayer layer];
	gLabel.font = (CFTypeRef)labelFont();
	gLabel.fontSize = FONT_SIZE;
	gLabel.foregroundColor = [NSColor colorWithWhite:1.0 alpha:0.85].CGColor;
	[gContent addSublayer:gLabel];

	gMeter = [CALayer layer];
	[gContent addSublayer:gMeter];

	for (int i = 0; i < NBARS; i++) {
		gBars[i] = [CALayer layer];
		gBars[i].cornerRadius = 1.5;
		[gMeter addSublayer:gBars[i]];
	}

	applyAccent();

	NSView *v = [[NSView alloc] initWithFrame:NSZeroRect];
	v.layer = root;
	v.wantsLayer = YES;
	gPanel.contentView = v;

	syncToScreen();
	CGPathRef collapsed = collapsedPath();
	gShape.path = collapsed;
	CGPathRelease(collapsed);
}

void zeeOverlayShow(void) {
	onMain(^{
		[NSApplication sharedApplication];
		buildPanel();
		if (gVisible) return;
		gVisible = YES;
		syncToScreen();

		// Start from an empty meter — otherwise the panel reopens still showing
		// the tail of the previous recording.
		memset(gLevels, 0, sizeof(gLevels));
		[CATransaction begin];
		[CATransaction setDisableActions:YES];
		layoutBars();
		[CATransaction commit];

		[gPanel orderFrontRegardless];

		// Phase 1: grow the shape out of the collapsed notch.
		CGPathRef collapsed = collapsedPath(), expanded = expandedPath();
		gShape.path = collapsed;
		[gShape removeAnimationForKey:@"path"];
		[CATransaction begin];
		[CATransaction setAnimationDuration:GROW_DUR];
		[CATransaction setAnimationTimingFunction:[CAMediaTimingFunction functionWithName:kCAMediaTimingFunctionEaseOut]];
		gShape.path = expanded;
		[CATransaction commit];
		CGPathRelease(collapsed);
		CGPathRelease(expanded);

		// Phase 2: fade the contents in over the tail of the expansion.
		dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(FADE_AFTER * NSEC_PER_SEC)), dispatch_get_main_queue(), ^{
			if (!gVisible) return;
			[CATransaction begin];
			[CATransaction setAnimationDuration:FADE_DUR];
			gContent.opacity = 1;
			[CATransaction commit];
		});
	});
}

void zeeOverlayHide(void) {
	onMain(^{
		if (!gPanel || !gVisible) return;
		gVisible = NO;

		[CATransaction begin];
		[CATransaction setAnimationDuration:0.15];
		gContent.opacity = 0;
		[CATransaction commit];

		dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(0.1 * NSEC_PER_SEC)), dispatch_get_main_queue(), ^{
			if (gVisible) return;
			CGPathRef collapsed = collapsedPath();
			[CATransaction begin];
			[CATransaction setAnimationDuration:0.3];
			[CATransaction setAnimationTimingFunction:[CAMediaTimingFunction functionWithName:kCAMediaTimingFunctionEaseIn]];
			[CATransaction setCompletionBlock:^{
				if (!gVisible) [gPanel orderOut:nil];
			}];
			gShape.path = collapsed;
			[CATransaction commit];
			CGPathRelease(collapsed);
		});
	});
}

void zeeOverlaySetState(const char *label, double r, double g, double b, int showMeter) {
	NSString *text = [NSString stringWithUTF8String:label];
	onMain(^{
		buildPanel();
		CGColorRef c = CGColorRetain([NSColor colorWithSRGBRed:r green:g blue:b alpha:1.0].CGColor);
		CGColorRelease(gAccent);
		gAccent = c;
		gMeterShown = showMeter != 0;

		[CATransaction begin];
		[CATransaction setAnimationDuration:0.25];
		applyAccent();
		layoutBars(); // repaint with the new ramp; no more levels may arrive
		gMeter.opacity = gMeterShown ? 1 : 0;
		if (gVisible) {
			// Drop the panel's lower half along with the bars, so hiding them
			// leaves a shorter pill rather than an empty gap.
			CGPathRef p = expandedPath();
			gShape.path = p;
			CGPathRelease(p);
		}
		[CATransaction commit];

		[CATransaction begin];
		[CATransaction setDisableActions:YES];
		layoutStatus(text);
		[CATransaction commit];
	});
}

void zeeOverlayPushLevel(float v) {
	onMain(^{
		if (!gPanel) return;
		memmove(gLevels, gLevels + 1, sizeof(gLevels) - sizeof(gLevels[0]));
		gLevels[NBARS - 1] = v;

		[CATransaction begin];
		[CATransaction setAnimationDuration:0.08];
		[CATransaction setAnimationTimingFunction:[CAMediaTimingFunction functionWithName:kCAMediaTimingFunctionEaseOut]];
		layoutBars();
		[CATransaction commit];
	});
}

void zeeOverlayRun(void) {
	@autoreleasepool {
		NSApplication *app = [NSApplication sharedApplication];
		[app setActivationPolicy:NSApplicationActivationPolicyAccessory];
		[app run];
	}
}
