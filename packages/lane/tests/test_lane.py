"""通道判定的回归: 人类通道要令牌, Agent 通道要项目名, 缺一不可."""

import lane


def test_agent_requires_project():
    """缺项目名时 Agent 通道被拒, 且给出稳定错误码。"""
    assert lane.decide("agent").ok is False
    assert lane.decide("agent").code == "project_required"
    assert lane.decide("agent", project="Bad Name!").code == "project_required"


def test_agent_project_normalized():
    """项目名归一化成分组键。"""
    got = lane.decide("agent", project="  Demo_App  ")
    assert got.ok is True
    assert got.project == "demo-app"


def test_human_requires_local_token(tmp_path):
    """人类通道没令牌不放行, 有本机令牌才放行。"""
    missing = lane.decide("human", path=tmp_path / "none.token")
    assert missing.ok is False
    assert missing.code == "human_token_required"
    token = lane.issue_human_token(tmp_path / "human.token")
    assert lane.decide("human", token=token, path=tmp_path / "human.token").ok is True
    assert lane.decide("human", token=token + "x", path=tmp_path / "human.token").ok is False


def test_human_token_not_accepted_for_agent_lane():
    """人类令牌不能替代项目名, 两条通道互不顶替。"""
    got = lane.decide("agent", token="whatever", project="")
    assert got.ok is False
    assert got.code == "project_required"


def test_cli_issue_and_check_never_echo_token(tmp_path, capsys):
    '''CLI 生成的令牌权限为 0600, check 能查到, 输出里不含令牌本身'''
    file = tmp_path / 'human.token'
    assert lane.main(['--path', str(file), 'check']) == 1
    assert lane.main(['--path', str(file), 'issue']) == 0
    assert file.stat().st_mode & 0o777 == 0o600
    assert lane.main(['--path', str(file), 'check']) == 0
    token = lane.read_human_token(file)
    assert token and token not in capsys.readouterr().out
