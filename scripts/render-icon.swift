// Draws the macOS app icon: the logo on a rounded square (light or dark, a variant) with a soft glow, a beam and a shadow, once per
// pixel size, vector-scaled (not a downsized bitmap), so the small sizes stay crisp.
// Run by scripts/make-app-icon.py, which owns every colour and measure (the JSON file).
//
//   swift scripts/render-icon.swift <logo.svg> <params.json> <out-dir> <px>...
// Writes <out-dir>/icon_<px>.png for each size. Uses AppKit only: nothing to install.
import AppKit

let args = CommandLine.arguments
guard args.count >= 5,
      let params = (try? JSONSerialization.jsonObject(with: Data(contentsOf: URL(fileURLWithPath: args[2])))) as? [String: Any],
      let logo = NSImage(contentsOfFile: args[1])
else {
    FileHandle.standardError.write(Data("usage: render-icon.swift <logo.svg> <params.json> <out-dir> <px>...\n".utf8))
    exit(2)
}
func num(_ k: String) -> CGFloat { CGFloat((params[k] as! NSNumber).doubleValue) }
func color(_ k: String) -> NSColor {
    let hex = (params[k] as! String).dropFirst()
    let v = UInt32(hex, radix: 16)!
    return NSColor(srgbRed: CGFloat((v >> 16) & 255) / 255, green: CGFloat((v >> 8) & 255) / 255, blue: CGFloat(v & 255) / 255, alpha: 1)
}

let canvas = num("canvas"), shape = num("shape"), n = num("squircle_exponent")
let box = (params["logo_box"] as! [NSNumber]).map { CGFloat($0.doubleValue) }  // x, y, w, h in SVG units (y down)

/// A rounded square whose corners are continuous curves (a superellipse), like the system's icon shape.
func squircle(_ r: CGRect) -> NSBezierPath {
    let p = NSBezierPath()
    let a = r.width / 2, b = r.height / 2, steps = 360
    for i in 0...steps {
        let t = CGFloat(i) / CGFloat(steps) * 2 * .pi
        let c = cos(t), s = sin(t)
        let x = r.midX + a * (c < 0 ? -1 : 1) * pow(abs(c), 2 / n)
        let y = r.midY + b * (s < 0 ? -1 : 1) * pow(abs(s), 2 / n)
        i == 0 ? p.move(to: CGPoint(x: x, y: y)) : p.line(to: CGPoint(x: x, y: y))
    }
    p.close()
    return p
}

for arg in args.dropFirst(4) {
    let px = Int(arg)!
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: px, pixelsHigh: px, bitsPerSample: 8, samplesPerPixel: 4,
                               hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    let ctx = NSGraphicsContext(bitmapImageRep: rep)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = ctx
    let cg = ctx.cgContext
    cg.scaleBy(x: CGFloat(px) / canvas, y: CGFloat(px) / canvas)  // draw in canvas units
    let inset = (canvas - shape) / 2
    let rect = CGRect(x: inset, y: inset, width: shape, height: shape)
    let path = squircle(rect)

    // Shadow, cast by the filled shape.
    cg.saveGState()
    cg.setShadow(offset: CGSize(width: 0, height: -num("shadow_dy")), blur: num("shadow_blur"),
                 color: NSColor.black.withAlphaComponent(num("shadow_alpha")).cgColor)
    color("bg_bottom").setFill()
    path.fill()
    cg.restoreGState()

    // Background gradient, then the light: everything below is clipped to the shape.
    cg.saveGState()
    path.addClip()
    NSGradient(starting: color("bg_top"), ending: color("bg_bottom"))!.draw(in: rect, angle: 270)

    let cx = canvas / 2 + num("logo_dx"), cy = canvas / 2 + num("logo_dy")
    /// A soft blob of light: a radial gradient, stretched to an ellipse, fading to nothing.
    func glow(_ c: NSColor, _ x: CGFloat, _ y: CGFloat, _ rx: CGFloat, _ ry: CGFloat, _ alpha: CGFloat) {
        cg.saveGState()
        cg.translateBy(x: x, y: y)
        cg.scaleBy(x: rx, y: ry)
        let g = CGGradient(colorsSpace: CGColorSpaceCreateDeviceRGB(),
                           colors: [c.withAlphaComponent(alpha).cgColor, c.withAlphaComponent(alpha * 0.4).cgColor, c.withAlphaComponent(0).cgColor] as CFArray,
                           locations: [0, 0.45, 1])!
        cg.drawRadialGradient(g, startCenter: .zero, startRadius: 0, endCenter: .zero, endRadius: 1, options: [])
        cg.restoreGState()
    }
    // Orbit lines: two faint tilted ellipses around the logo, like the landing page's rings.
    cg.saveGState()
    cg.translateBy(x: cx, y: cy)
    cg.rotate(by: 0.5)
    color("ring").withAlphaComponent(num("ring_alpha")).setStroke()
    for (rx, ry) in [(shape * 0.50, shape * 0.34), (shape * 0.40, shape * 0.52)] {
        let o = NSBezierPath(ovalIn: CGRect(x: -rx, y: -ry, width: rx * 2, height: ry * 2))
        o.lineWidth = 2.5
        o.stroke()
    }
    cg.restoreGState()
    // A cool halo behind the logo, then the beam: cyan near the logo, violet, coral at the edge.
    let by = cy + num("beam_dy")
    let ga = num("glow_alpha"), ba = num("beam_alpha")
    glow(color("glow_cyan"), cx - 60, cy + 30, 330, 330, ga)
    glow(color("glow_violet"), cx - 20, cy - 60, 260, 260, ga * 0.6)
    glow(color("glow_cyan"), cx + 230, by + 14, 230, 80, ba)
    glow(color("glow_violet"), cx + 330, by - 6, 190, 62, ba * 0.7)
    glow(color("glow_coral"), cx + 400, by - 26, 210, 70, ba)

    // The edge: a hairline.
    path.lineWidth = num("rim_width")
    color("rim").withAlphaComponent(num("rim_alpha")).setStroke()
    path.stroke()
    cg.restoreGState()

    // The logo, its visible bounds (not the SVG's page) centred in the shape.
    let h = shape * num("logo_height")
    let w = h * box[2] / box[3]
    let dest = CGRect(x: canvas / 2 - w / 2 + num("logo_dx"), y: canvas / 2 - h / 2 + num("logo_dy"), width: w, height: h)
    let svg = logo.size.height  // the SVG's y axis points down, the image's up
    logo.draw(in: dest, from: CGRect(x: box[0], y: svg - box[1] - box[3], width: box[2], height: box[3]),
              operation: .sourceOver, fraction: 1)

    NSGraphicsContext.restoreGraphicsState()
    try rep.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: "\(args[3])/icon_\(px).png"))
}
