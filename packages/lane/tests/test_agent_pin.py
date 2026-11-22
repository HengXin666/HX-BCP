"""An api caller (x-api-key) is pinned to the agent lane, and the console unlock code is short lived.

Agent Note: .agents/notes/implemented/feature/2026-11-22-lane-convergence.md
"""

import lane


def test_api_key_can_not_claim_human(tmp_path):
    """Even with a valid token or loopback, a request carrying x-api-key is refused the human lane."""
    file = tmp_path / "human.token"
    token = lane.issue_human_token(file)
    got = lane.decide("human", token=token, path=file, agent_credential=True)
    assert got.ok is False and got.code == "lane_forbidden"
    assert lane.decide("human", loopback=True, path=file, agent_credential=True).ok is False
    assert lane.decide("", project="crawler-a", agent_credential=True).ok is True


def test_unlock_code_lifetime(tmp_path):
    """The unlock code never holds the token, expires after UNLOCK_TTL_S and dies with a new token."""
    file = tmp_path / "human.token"
    token = lane.issue_human_token(file)
    code = lane.unlock_code(file, now=1000)
    assert token not in code
    assert lane.unlock_valid(code, file, now=1000 + lane.UNLOCK_TTL_S) is True
    assert lane.unlock_valid(code, file, now=1001 + lane.UNLOCK_TTL_S) is False
    fresh = lane.unlock_code(file)
    lane.issue_human_token(file)
    assert lane.unlock_valid(fresh, file) is False
    assert lane.unlock_code(tmp_path / "none.token") == ""
