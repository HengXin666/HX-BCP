"""Loopback rule of the human lane: socket peer on loopback plus a loopback Host, never a header.

Agent Note: .agents/notes/implemented/feature/2026-11-01-loopback-human.md
"""

import lane


def test_loopback_peers():
    """127.0.0.0/8, ::1 and the v4 mapped form are loopback, everything else is not."""
    for peer in ("127.0.0.1", "127.8.9.10", "::1", "::ffff:127.0.0.1"):
        assert lane.is_loopback(peer) is True, peer
    for peer in ("", "testclient", "10.0.0.5", "192.168.1.2", "::ffff:10.0.0.5", "fe80::1", "localhost", "0.0.0.0"):
        assert lane.is_loopback(peer) is False, peer


def test_host_must_name_this_machine():
    """Host narrows the rule against DNS rebinding, it never admits a remote peer by itself."""
    for host in ("127.0.0.1:8325", "localhost:8325", "LOCALHOST", "[::1]:8325", "127.0.0.1"):
        assert lane.host_is_loopback(host) is True, host
    for host in ("", "evil.example:8325", "evil.example", "192.168.1.2:8325", "[fe80::1]:8325", "localhost.evil.example"):
        assert lane.host_is_loopback(host) is False, host
    assert lane.loopback_admitted("127.0.0.1", "127.0.0.1:8325") is True
    assert lane.loopback_admitted("127.0.0.1", "evil.example:8325") is False
    assert lane.loopback_admitted("192.168.1.2", "127.0.0.1:8325") is False


def test_loopback_admits_human_without_token(tmp_path):
    """Loopback is one of three alternatives, recorded in audit by, and needs no token file."""
    none = tmp_path / "none.token"
    got = lane.decide("human", loopback=True, path=none)
    assert got.ok is True and got.lane == lane.HUMAN and got.audit["by"] == "loopback"
    assert lane.decide("human", loopback=False, path=none).code == "human_token_required"
    file = tmp_path / "human.token"
    token = lane.issue_human_token(file)
    assert lane.decide("human", token=token, path=file).audit["by"] == "token"
    assert lane.decide("human", token="fake", path=file).ok is False


def test_loopback_does_not_merge_lanes():
    """Without x-bcp-lane: human a loopback caller is still agent and still needs a project."""
    got = lane.decide("", loopback=True)
    assert got.ok is False and got.lane == lane.AGENT and got.code == "project_required"
    assert lane.decide("agent", loopback=True, project="crawler-a").lane == lane.AGENT
