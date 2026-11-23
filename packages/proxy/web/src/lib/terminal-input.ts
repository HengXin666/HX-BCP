// Terminal input framing.
//
// xterm.js hands the page one onData callback per user gesture, and a paste is
// a single large callback. The page used to forward that callback as one
// WebSocket text frame, so pasting a few hundred KB produced a frame larger
// than the server's read limit; coder/websocket answers an over-limit frame by
// closing the whole connection with StatusMessageTooBig, which the browser sees
// as "终端连接不稳定" rather than as a rejected paste.
//
// The fix is to keep every frame bounded on the client: TERMINAL_INPUT_CHUNK_BYTES
// is deliberately far below the server's terminalReadLimit (256 KiB) so the
// JSON envelope, a future header, or a larger PDU never turns a paste into a
// dropped session.

/** Maximum UTF-8 byte length of one outgoing terminal input frame. */
export const TERMINAL_INPUT_CHUNK_BYTES = 32 << 10

// One codec pair for the page: chunking runs on every keystroke, so it must not
// allocate codecs per call.
const terminalInputEncoder = new TextEncoder()
const terminalInputDecoder = new TextDecoder("utf-8")

/**
 * Split terminal input into frames of at most `maxBytes` UTF-8 bytes.
 *
 * Returns a single-element array for any normal keystroke burst (the common
 * case) and an empty array for empty input. A split never lands inside a code
 * point — the byte budget is shrunk to the last character boundary at or below
 * it — so every chunk is a valid string and the concatenation of the returned
 * chunks is exactly the original input.
 */
export function chunkTerminalInput(data: string, maxBytes: number = TERMINAL_INPUT_CHUNK_BYTES): string[] {
  if (data === "") return []
  const limit = Number.isFinite(maxBytes) && maxBytes >= 1 ? Math.floor(maxBytes) : TERMINAL_INPUT_CHUNK_BYTES

  const encoded = terminalInputEncoder.encode(data)
  if (encoded.length <= limit) return [data]

  const chunks: string[] = []
  let offset = 0
  while (offset < encoded.length) {
    let end = Math.min(offset + limit, encoded.length)
    // A byte with the top two bits set to 10 is a UTF-8 continuation byte, so
    // stepping back over them stops the split at a character boundary. The
    // bound is end < encoded.length, so the index is always in range.
    while (end > offset + 1 && end < encoded.length && ((encoded[end] ?? 0) & 0xc0) === 0x80) {
      end -= 1
    }
    chunks.push(terminalInputDecoder.decode(encoded.subarray(offset, end)))
    offset = end
  }
  return chunks
}

// --- Keystroke batching -----------------------------------------------------
//
// Sending one WebSocket frame per xterm onData callback makes typing feel
// laggy on a weak round trip, so safe keystrokes are collapsed into a short
// window. The batching state lives in a small factory rather than inside the
// page component so the framing rule is unit testable: every socket.send goes
// through chunkTerminalInput, and a control byte (Enter, Ctrl-C, Backspace,
// arrows) or a lapsed window flushes immediately.

/** Keystrokes are collapsed into at most one frame per window. */
export const TERMINAL_INPUT_FLUSH_MS = 12
/** Soft threshold: accumulate at most this much before flushing early. */
export const TERMINAL_INPUT_BATCH_LIMIT = 1024

/** Matches the original page rule: control bytes echo immediately. */
const terminalImmediateInput = /[\x00-\x1f]/

export function createInputFramer(options: {
  send: (frame: string) => void
  schedule: (callback: () => void, delayMs: number) => unknown
  cancel: (handle: unknown) => void
  chunkBytes?: number
  flushMs?: number
  batchLimit?: number
}) {
  const chunkBytes = options.chunkBytes ?? TERMINAL_INPUT_CHUNK_BYTES
  const flushMs = options.flushMs ?? TERMINAL_INPUT_FLUSH_MS
  const batchLimit = options.batchLimit ?? TERMINAL_INPUT_BATCH_LIMIT
  let pending = ""
  let timer: unknown = null

  const flush = () => {
    if (timer !== null) {
      options.cancel(timer)
      timer = null
    }
    if (pending === "") return
    const frames = chunkTerminalInput(pending, chunkBytes)
    pending = ""
    for (const frame of frames) options.send(frame)
  }

  return {
    /** Feed one xterm onData callback. */
    push(data: string) {
      if (data === "") return
      pending += data
      const immediate = data === "\x7f" || terminalImmediateInput.test(data)
      if (immediate || pending.length >= batchLimit) {
        flush()
      } else if (timer === null) {
        timer = options.schedule(flush, flushMs)
      }
    },
    flush,
    /** Drop the pending batch without sending (used when a socket dies). */
    reset() {
      if (timer !== null) {
        options.cancel(timer)
        timer = null
      }
      pending = ""
    },
    get pendingLength() {
      return pending.length
    },
  }
}
