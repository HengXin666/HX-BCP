"""Console session of the human lane: hmac cookie, never the token, only from same-origin pages."""

import lane


def test_session_admits_only_from_same_origin(tmp_path):
    """A valid session admits the human lane only together with the same-origin signal."""
    file = tmp_path / "human.token"
    token = lane.issue_human_token(file)
    value = lane.issue_session(file)
    assert token not in value
    assert lane.decide("human", session=value, from_self=True, path=file).ok is True
    assert lane.decide("human", session=value, from_self=False, path=file).ok is False
    assert lane.decide("human", session="1.bad", from_self=True, path=file).ok is False


def test_session_expires_and_dies_with_new_token(tmp_path):
    """Sessions older than TOKEN_TTL_S or issued under an old token are refused."""
    file = tmp_path / "human.token"
    lane.issue_human_token(file)
    old = lane.issue_session(file, now=1000)
    assert lane.session_valid(old, file, now=1000 + lane.TOKEN_TTL_S) is True
    assert lane.session_valid(old, file, now=1001 + lane.TOKEN_TTL_S) is False
    fresh = lane.issue_session(file)
    lane.issue_human_token(file)
    assert lane.session_valid(fresh, file) is False
    assert lane.issue_session(tmp_path / "none.token") == ""


def test_same_origin_signal():
    """Sec-Fetch-Site decides when present, else Origin must equal the request host."""
    assert lane.same_origin("127.0.0.1:8325", "", "same-origin") is True
    assert lane.same_origin("127.0.0.1:8325", "http://127.0.0.1:8325", "same-site") is False
    assert lane.same_origin("127.0.0.1:8325", "http://127.0.0.1:8325", "") is True
    assert lane.same_origin("127.0.0.1:8325", "http://localhost:9999", "") is False
    assert lane.same_origin("127.0.0.1:8325", "", "") is False
