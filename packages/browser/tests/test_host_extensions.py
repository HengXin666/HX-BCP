"""宿主外部扩展不进副本: 每个 Chromium 副本都带 --disable-default-apps."""

from hx_browser import launch_hooks
from hx_browser.models import Environment


def test_chromium_args_block_host_external_extensions():
    env = Environment(name="t")
    assert launch_hooks.NO_HOST_EXTENSIONS_FLAG in launch_hooks.chromium_args(env)


def test_flag_is_disable_default_apps():
    # Chromium external_provider_impl.cc: 带此开关时在创建外部清单 provider 之前返回
    assert launch_hooks.NO_HOST_EXTENSIONS_FLAG == "--disable-default-apps"
