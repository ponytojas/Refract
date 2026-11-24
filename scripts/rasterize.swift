// Renders an SVG to a PNG of an exact pixel size with AppKit (no rsvg, ImageMagick or Inkscape needed).
// Used for the DMG background (scripts/make-dmg-background.py).
//
//   swift scripts/rasterize.swift <in.svg> <out.png> <width> <height>
import AppKit

let a = CommandLine.arguments
guard a.count == 5, let w = Int(a[3]), let h = Int(a[4]), let img = NSImage(contentsOfFile: a[1]) else {
    FileHandle.standardError.write(Data("usage: rasterize.swift <in.svg> <out.png> <width> <height>\n".utf8))
    exit(2)
}
let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: w, pixelsHigh: h, bitsPerSample: 8, samplesPerPixel: 4,
                           hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
NSGraphicsContext.saveGraphicsState()
NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
img.draw(in: NSRect(x: 0, y: 0, width: w, height: h))
NSGraphicsContext.restoreGraphicsState()
try rep.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: a[2]))
