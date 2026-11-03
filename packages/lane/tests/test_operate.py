"""may_operate and group_key regression, human and agent sessions stay apart."""

import lane


def _agent(project):
    return lane.decide("agent", project=project)


def _human(tmp_path):
    token = lane.issue_human_token(tmp_path / "human.token")
    return lane.decide("human", token=token, path=tmp_path / "human.token")


def test_agent_only_touches_own_project():
    d = _agent("crawler-a")
    assert lane.may_operate(d, "agent", "crawler-a", write=True) is True
    assert lane.may_operate(d, "agent", "Crawler_A", write=True) is True
    assert lane.may_operate(d, "agent", "shop-b", write=False) is False
    assert lane.may_operate(d, "human", "", write=False) is False


def test_human_reads_agent_but_never_writes(tmp_path):
    d = _human(tmp_path)
    assert lane.may_operate(d, "human", "", write=True) is True
    assert lane.may_operate(d, "agent", "crawler-a", write=False) is True
    assert lane.may_operate(d, "agent", "crawler-a", write=True) is False


def test_refused_decision_does_nothing():
    d = lane.decide("agent")
    assert d.ok is False
    assert lane.may_operate(d, "agent", "", write=False) is False


def test_group_key():
    assert lane.group_key("human") == "human"
    assert lane.group_key("agent", "Demo_App") == "agent:demo-app"
    assert lane.group_key("", "x") == "human"
