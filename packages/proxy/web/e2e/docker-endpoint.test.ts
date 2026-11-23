import { test } from "node:test"
import assert from "node:assert/strict"
import { dockerEndpoint, dockerProxyURI, dockerProxyURIs } from "../src/lib/docker-endpoint.ts"

test("container copy respects bind and protocol instead of public domain", () => {
  const listener = { kind: "mixed", bind_address: "0.0.0.0", port: 7890 }
  assert.equal(dockerEndpoint(listener, "172.17.0.1"), "172.17.0.1:7890")
  for (const bind_address of ["127.0.0.1", "127.0.0.2", "::1", "localhost", ""]) assert.equal(dockerEndpoint({ ...listener, bind_address }, "172.17.0.1"), null)
  for (const kind of ["vless", "vmess", "trojan"]) assert.equal(dockerEndpoint({ ...listener, kind }, "172.17.0.1"), null)
  assert.equal(dockerEndpoint(listener, ""), null)
  assert.equal(dockerEndpoint({ ...listener, bind_address: "192.168.1.5" }, "172.17.0.1"), "192.168.1.5:7890")
})

test("Docker URI retains escaped authentication and drops public TLS, path and fragment", () => {
  const content = "https://user%40name:p%3Aa%2Fss@edge.example.com:443/#display\nsocks5://user%40name:p%3Aa%2Fss@edge.example.com:443/#display\n"
  assert.equal(dockerProxyURI("172.17.0.1:7890", "mixed", content, true), "http://user%40name:p%3Aa%2Fss@172.17.0.1:7890")
  assert.equal(dockerProxyURI("[fd00::1]:7890", "socks", content, true), "socks5h://user%40name:p%3Aa%2Fss@[fd00::1]:7890")
  assert.equal(dockerProxyURI("172.17.0.1:7890", "http", "http://example.com:7890", false), "http://172.17.0.1:7890")
  assert.throws(() => dockerProxyURI("172.17.0.1:7890", "http", "http://example.com:7890", true))
  assert.throws(() => dockerProxyURI("172.17.0.1:7890", "http", "vless://id@example.com", true))
  assert.throws(() => dockerProxyURI("172.17.0.1:7890", "http", "<html>not a subscription</html>", true))
})

test("assessed Mixed entry yields HTTP and remote-DNS SOCKS URIs", () => {
  const content = "http://docker-ab12:secret@1.2.3.4:7890\nsocks5://docker-ab12:secret@1.2.3.4:7890\n"
  const uris = dockerProxyURIs({ host: "172.17.0.1", port: 17890, schemes: ["http", "socks5h"] }, content)
  assert.deepEqual(uris, [
    { scheme: "http", uri: "http://docker-ab12:secret@172.17.0.1:17890" },
    { scheme: "socks5h", uri: "socks5h://docker-ab12:secret@172.17.0.1:17890" },
  ])
})
