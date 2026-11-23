// Helpers for keeping the file panel and the shell cwd in sync.
//
// IMPORTANT: the panel never types a probe command into the user's shell.
// Sending `pwd` used to pollute the shell history with lines the user never
// typed, which is unacceptable for a real terminal. The directory now arrives
// as a server control frame:
//
//   - the control plane reads /proc/<shell pid>/cwd (or receives it from the
//     root PTY helper) and pushes a `{type:"cwd"}` frame whenever it changes;
//   - the shell is additionally configured to emit the standard OSC 7 sequence
//     after every prompt, which the terminal page parses as a local echo of the
//     same information.
//
// This module therefore only contains pure path helpers plus the OSC 7 parser.

/** Parent of an absolute path, mirroring the server's filepath.Dir semantics.
 * Returns null at the root so callers can disable the "go up" action. */
export function parentPath(path: string): string | null {
  const trimmed = path.replace(/\/+$/, "")
  if (trimmed === "" || trimmed === "/") return null
  const index = trimmed.lastIndexOf("/")
  if (index <= 0) return "/"
  return trimmed.slice(0, index)
}

/** Collapse duplicate slashes and resolve `.` / `..` lexically. Never returns empty. */
export function normalizePath(path: string): string {
  const absolute = path.startsWith("/")
  const segments = path.split("/")
  const resolved: string[] = []
  for (const segment of segments) {
    if (segment === "" || segment === ".") continue
    if (segment === "..") {
      if (resolved.length > 0) resolved.pop()
      continue
    }
    resolved.push(segment)
  }
  return absolute ? `/${resolved.join("/")}` : resolved.join("/") || "."
}

/** Wrap a path in single quotes, escaping embedded single quotes (POSIX-safe). */
export function quoteForShell(path: string): string {
  return `'${path.replace(/'/g, `'\\''`)}'`
}

// OSC 7 is the de-facto standard sequence a shell uses to report its working
// directory: ESC ] 7 ; file://<host><path> ST, where ST is BEL or ESC \.
// Every modern terminal (VS Code, WezTerm, Kitty, iTerm2, Tabby, ttyd) reads
// it, and our shell integration emits it from PROMPT_COMMAND / precmd.
//
// The terminal page buffers recent PTY output and runs this parser over it, so
// a sequence split across frames is still recognized.
const osc7Pattern = /\x1b\]7;(?:file:\/\/)?([^\x07\x1b]*)(?:\x07|\x1b\\)/g

/**
 * Extract the most recent OSC 7 directory from a chunk of PTY output. Returns
 * null when the chunk carries no complete sequence. The host part is ignored:
 * only the path is used, and it is normalized so `/` and trailing slashes are
 * consistent with the rest of the UI.
 */
export function detectOsc7Directory(chunk: string): string | null {
  let match: RegExpExecArray | null
  let found: string | null = null
  osc7Pattern.lastIndex = 0
  while ((match = osc7Pattern.exec(chunk)) !== null) {
    const raw = match[1] ?? ""
    // `file://host/path` puts the path after the first slash of the remainder;
    // a bare `file:///path` yields the path directly once the host is empty.
    const path = osc7Path(raw)
    if (path) found = path
  }
  return found
}

function osc7Path(value: string): string | null {
  let trimmed = value.trim()
  if (trimmed === "") return null
  // The capture starts right after "file://", so it still carries the host:
  // "host/path" for file://host/path and "/path" for file:///path. Drop
  // everything up to the first slash, which is the start of the path.
  if (!trimmed.startsWith("/")) {
    const slash = trimmed.indexOf("/")
    if (slash < 0) return null
    trimmed = trimmed.slice(slash)
  }
  // URL-decode %20 and friends so a directory with spaces round-trips.
  let decoded = trimmed
  try {
    decoded = decodeURIComponent(trimmed)
  } catch {
    decoded = trimmed
  }
  if (!decoded.startsWith("/")) return null
  return normalizePath(decoded)
}

// Keep the scanning buffer bounded: OSC 7 sequences are tiny, so a few KB of
// trailing output is always enough to catch a sequence split across frames.
const scanBufferLimit = 4096

/** Rolling PTY-output buffer used to detect OSC 7 sequences across frames. */
export function createOsc7Scanner() {
  let buffer = ""
  return {
    /** Feed decoded output; returns a directory when one was reported. */
    feed(text: string): string | null {
      buffer += text
      if (buffer.length > scanBufferLimit) buffer = buffer.slice(-scanBufferLimit)
      const directory = detectOsc7Directory(buffer)
      if (directory) {
        // Consume the buffer once a sequence was found: the shell only reports
        // a directory when it changed, so keeping the old one would re-emit the
        // same update on the next frame.
        buffer = ""
      }
      return directory
    },
    reset() {
      buffer = ""
    },
  }
}
