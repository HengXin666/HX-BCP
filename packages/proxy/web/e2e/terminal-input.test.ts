import assert from "node:assert/strict"
import test from "node:test"

import { chunkTerminalInput, createInputFramer, TERMINAL_INPUT_CHUNK_BYTES } from "../src/lib/terminal-input.ts"

// These tests pin the client half of the frame-size contract. The server's half
// (terminalReadLimit, 256 KiB) is pinned in internal/api/terminal_heartbeat_test.go;
// if the client ever stops chunking, a large paste closes the whole WebSocket
// with StatusMessageTooBig and the terminal looks "unstable" instead of
// reporting a rejected paste.

const encoder = new TextEncoder()

test("short input stays in a single frame", () => {
  const chunks = chunkTerminalInput("ls -la\n")
  assert.deepEqual(chunks, ["ls -la\n"])
})

test("empty input produces no frame", () => {
  assert.deepEqual(chunkTerminalInput(""), [])
})

test("a large ASCII paste is split below the chunk budget and round-trips", () => {
  const paste = "x".repeat(TERMINAL_INPUT_CHUNK_BYTES * 3 + 7)
  const chunks = chunkTerminalInput(paste)
  assert.ok(chunks.length >= 4, "expected at least four chunks, got " + chunks.length)
  for (const chunk of chunks) {
    assert.ok(encoder.encode(chunk).length <= TERMINAL_INPUT_CHUNK_BYTES, "chunk exceeded the byte budget")
  }
  assert.equal(chunks.join(""), paste)
})

test("multi-byte characters split on code point boundaries and round-trip", () => {
  // 3-byte characters with a chunk budget that cannot align with the character
  // width: a naive substring split would cut a code point in half.
  const text = "中文终端".repeat(64)
  const chunks = chunkTerminalInput(text, 10)
  assert.equal(chunks.join(""), text)
  for (const chunk of chunks) {
    assert.ok(!chunk.includes("\ufffd"), "split produced a replacement character")
    assert.ok(encoder.encode(chunk).length <= 10, "chunk exceeded the byte budget")
  }
})

test("astral characters (4-byte UTF-8) survive chunking", () => {
  const text = "🙂".repeat(500)
  const chunks = chunkTerminalInput(text, 7)
  assert.equal(chunks.join(""), text)
  assert.ok(!chunks.join("").includes("\ufffd"))
})

test("a payload larger than the server read limit is still split into safe frames", () => {
  // 300 KiB: larger than the server's 256 KiB terminalReadLimit, which is the
  // paste that used to kill the socket. Every emitted frame must stay under the
  // client budget regardless.
  const paste = "a".repeat(300 << 10)
  const chunks = chunkTerminalInput(paste)
  assert.equal(chunks.join(""), paste)
  const largest = Math.max(...chunks.map((chunk) => encoder.encode(chunk).length))
  assert.ok(largest <= TERMINAL_INPUT_CHUNK_BYTES, "largest frame " + largest + " exceeded the budget")
})

test("a budget below one code point still round-trips losslessly", () => {
  // 3-byte characters with a 4-byte budget: splits land on character
  // boundaries, so the concat is lossless even though characters never align
  // with the budget. (No budget can make a lone byte of a code point valid,
  // so the default budget is many code points wide by construction.)
  const chunks = chunkTerminalInput("中文终端", 4)
  assert.equal(chunks.join(""), "中文终端")
  assert.ok(!chunks.join("").includes("\ufffd"))
})

// --- Framer tests -----------------------------------------------------------

function fakeClock() {
  let nextHandle = 1
  const scheduled = new Map<unknown, () => void>()
  return {
    schedule(callback: () => void) {
      const handle = nextHandle++
      scheduled.set(handle, callback)
      return handle
    },
    cancel(handle: unknown) {
      scheduled.delete(handle)
    },
    /** Run every callback scheduled so far, like one event-loop turn. */
    run() {
      const callbacks = [...scheduled.values()]
      scheduled.clear()
      for (const callback of callbacks) callback()
    },
    get size() {
      return scheduled.size
    },
  }
}

test("a keystroke burst leaves as one frame", () => {
  const sent: string[] = []
  const clock = fakeClock()
  const framer = createInputFramer({ send: (frame) => sent.push(frame), schedule: clock.schedule, cancel: clock.cancel })
  framer.push("l")
  framer.push("s")
  assert.deepEqual(sent, [], "keystrokes must be batched, not sent per callback")
  clock.run()
  assert.deepEqual(sent, ["ls"])
  assert.equal(framer.pendingLength, 0)
})

test("a control byte flushes immediately", () => {
  const sent: string[] = []
  const clock = fakeClock()
  const framer = createInputFramer({ send: (frame) => sent.push(frame), schedule: clock.schedule, cancel: clock.cancel })
  framer.push("echo hi")
  framer.push("\r")
  assert.deepEqual(sent, ["echo hi\r"], "Enter must not wait for the batch window")
  assert.equal(clock.size, 0, "the pending timer must be cancelled by an immediate flush")
  clock.run()
  assert.deepEqual(sent, ["echo hi\r"], "a cancelled timer must not re-send the batch")
})

test("a large paste is split into bounded frames", () => {
  const sent: string[] = []
  const clock = fakeClock()
  const framer = createInputFramer({
    send: (frame) => sent.push(frame),
    schedule: clock.schedule,
    cancel: clock.cancel,
    chunkBytes: 64,
  })
  const paste = "a".repeat(200)
  framer.push(paste)
  clock.run()
  assert.ok(sent.length >= 4, "expected the paste to be chunked, got " + sent.length + " frame(s)")
  for (const frame of sent) {
    assert.ok(encoder.encode(frame).length <= 64, "a frame exceeded the byte budget")
  }
  assert.equal(sent.join(""), paste)
})

// This is the regression this whole module exists for: one frame larger than
// the server read limit used to close the socket with StatusMessageTooBig.
test("a paste larger than the server read limit never produces an oversized frame", () => {
  const sent: string[] = []
  const clock = fakeClock()
  const framer = createInputFramer({ send: (frame) => sent.push(frame), schedule: clock.schedule, cancel: clock.cancel })
  framer.push("b".repeat(300 << 10))
  clock.run()
  const largest = Math.max(...sent.map((frame) => encoder.encode(frame).length))
  assert.ok(largest <= TERMINAL_INPUT_CHUNK_BYTES, "largest frame " + largest + " exceeded the budget")
  assert.equal(sent.join("").length, 300 << 10)
})

test("reset drops the pending batch and its timer", () => {
  const sent: string[] = []
  const clock = fakeClock()
  const framer = createInputFramer({ send: (frame) => sent.push(frame), schedule: clock.schedule, cancel: clock.cancel })
  framer.push("ls")
  framer.reset()
  clock.run()
  assert.deepEqual(sent, [])
})
