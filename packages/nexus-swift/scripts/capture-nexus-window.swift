#!/usr/bin/env swift
import AppKit

let info = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]] ?? []
var found: UInt32?
for w in info {
    guard let name = w[kCGWindowOwnerName as String] as? String,
          name == "Nexus" || name == "NexusApp",
          let layer = w[kCGWindowLayer as String] as? Int,
          layer == 0,
          let id = w[kCGWindowNumber as String] as? UInt32
    else { continue }
    let bounds = w[kCGWindowBounds as String] as? [String: CGFloat]
    let width = bounds?["Width"] ?? 0
    if width > 200 { found = id; break }
}
guard let wid = found else {
    fputs("no NexusApp window\n", stderr)
    exit(1)
}
print(wid)
