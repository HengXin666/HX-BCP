import assert from "node:assert/strict"
import test from "node:test"

import { createOsc7Scanner, detectOsc7Directory, normalizePath, parentPath, quoteForShell } from "../src/lib/terminal-cwd.ts"

test("normalizePath collapses slashes and resolves dot segments", () => {
  assert.equal(normalizePath("//home//user/"), "/home/user")
  assert.equal(normalizePath("/home/user/../docs"), "/home/docs")
  assert.equal(normalizePath("home/./docs"), "home/docs")
  assert.equal(normalizePath("/"), "/")
  assert.equal(normalizePath(""), ".")
})

test("quoteForShell wraps paths safely", () => {
  assert.equal(quoteForShell("/home/user"), "'/home/user'")
  assert.equal(quoteForShell("/home/It's"), `'/home/It'\\''s'`)
})

test("parentPath mirrors server filepath.Dir semantics", () => {
  assert.equal(parentPath("/"), null)
  assert.equal(parentPath("/root"), "/")
  assert.equal(parentPath("/home/user/docs"), "/home/user")
  assert.equal(parentPath("/home/user/"), "/home")
  assert.equal(parentPath(""), null)
})

// The panel follows the shell through OSC 7 and the server-side /proc read.
// These tests pin the parser, because a regression here would make the panel
// silently stop following cd — and the previous "fix" for that was to type pwd
// into the user's shell, which is exactly what we must never do again.
test("detectOsc7Directory reads the standard BEL-terminated sequence", () => {
  assert.equal(detectOsc7Directory("\x1b]7;file://host/home/user\x07"), "/home/user")
  assert.equal(detectOsc7Directory("prompt\x1b]7;file://host/\x07$ "), "/")
})

test("detectOsc7Directory reads the ESC-backslash terminated sequence", () => {
  assert.equal(detectOsc7Directory("\x1b]7;file://host/var/www\x1b\\"), "/var/www")
})

test("detectOsc7Directory decodes percent-escaped paths", () => {
  assert.equal(detectOsc7Directory("\x1b]7;file://host/home/a%20b\x07"), "/home/a b")
})

test("detectOsc7Directory ignores unrelated output", () => {
  assert.equal(detectOsc7Directory("plain output\n"), null)
  assert.equal(detectOsc7Directory("\x1b]0;title\x07"), null)
  assert.equal(detectOsc7Directory(""), null)
})

test("detectOsc7Directory returns the last sequence in a chunk", () => {
  const chunk = "\x1b]7;file://host/home\x07filler\x1b]7;file://host/tmp\x07"
  assert.equal(detectOsc7Directory(chunk), "/tmp")
})

test("osc7 scanner reassembles a sequence split across frames", () => {
  const scanner = createOsc7Scanner()
  assert.equal(scanner.feed("\x1b]7;file://host/home/us"), null)
  assert.equal(scanner.feed("er/docs\x07"), "/home/user/docs")
})

test("osc7 scanner does not re-report a consumed directory", () => {
  const scanner = createOsc7Scanner()
  assert.equal(scanner.feed("\x1b]7;file://host/tmp\x07"), "/tmp")
  assert.equal(scanner.feed("more output"), null)
})

test("osc7 scanner stays quiet on plain output", () => {
  const scanner = createOsc7Scanner()
  for (const chunk of ["total 4\n", "drwxr-xr-x 2 root root\n", "$ "]) {
    assert.equal(scanner.feed(chunk), null)
  }
})
