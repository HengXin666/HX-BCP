# Agent Note: HX-Browser 时区兜底把 resolvedOptions 定义成 getter 导致页面 Intl 全崩

Status: implemented

## Problem

指纹扩展(`services/hx-browser/src/hx_browser/fingerprint.py` 的 `_CONTENT_JS`)在
`FP.timezone_offset !== undefined` 时给 `Intl.DateTimeFormat.prototype.resolvedOptions`
做覆写, 写法是:

```js
def(Intl.DateTimeFormat.prototype, 'resolvedOptions', function() {
  const r = Intl.DateTimeFormat.prototype.resolvedOptions.call(this);
  ...
});
```

`def` 的实现是 `Object.defineProperty(obj, key, {get: getter})`, 即把方法变成**访问器**。
于是两个后果同时成立:

1. getter 体内再次读 `Intl.DateTimeFormat.prototype.resolvedOptions` → 又触发 getter, 递归
   (`RangeError: Maximum call stack size exceeded`)。
2. 即便递归不炸, 访问器返回的是 options 对象而不是函数, 页面里
   `new Intl.DateTimeFormat().resolvedOptions()` 直接 `TypeError: resolvedOptions is not a function`。

实测场景: opencode.ai 注册页提交后 hCaptcha 取指纹时整页 Intl 抛错; 我们自己的
`page.evaluate` 也读不到 locale/timeZone。

## Decision

先取原始函数, 再用**数据属性**包一层, 保持"方法"语义:

```js
const _ro = Intl.DateTimeFormat.prototype.resolvedOptions;
Object.defineProperty(Intl.DateTimeFormat.prototype, 'resolvedOptions', {
  configurable: true, writable: true,
  value: function() { const r = _ro.call(this); try { r.timeZone = tzName; } catch (e) {} return r; }
});
```

同时把外层判据从 `FP.timezone_offset !== undefined` 改成 `!= null`: offset 为 null 时
不该注入一个恒返回 null 的 `getTimezoneOffset`(同样是"有值但错")。

Chromium 侧真正的时区来源是启动时的 `TZ` 环境变量(`browser.py` 已设置),
JS 覆写只是兜底, 所以修好它不改变时区行为, 只消除异常面。

## Alternatives considered

- **整段删掉 JS 时区兜底**: 否决。Firefox 路径与未设 `TZ` 的场景仍需要兜底;
  而且删掉会让"TZ 未设"与"TZ 已设"两种环境行为分叉, 判据更难对齐。
- **用 `def` 返回函数(getter 返回 function)**: 可行但更绕 —— 每次读属性都新建函数,
  `fn.toString()` 与 `Function.prototype.toString` 检测面反而更脏。数据属性最接近原生。

## Verification

```text
修复前: new Intl.DateTimeFormat().resolvedOptions() -> TypeError / RangeError
修复后: locale=en-US tz=America/New_York offset=240, 无异常
```

复测环境: env_52663b9fde (Windows UA / Win32 / en-US / America/New_York / headless),
页面 https://opencode.ai/console/signup。

## Consequences

- 异常面消失: `resolvedOptions` 不再递归调用自身, 注入后不再抛 RangeError;
  时区取值仍以启动时的 `TZ` 为准, JS 覆写只是兜底, 行为不变。
- `FP.timezone_offset` 为 `null` 时不再注入恒返回 `null` 的 `getTimezoneOffset`,
  少了"有值但错"这一类假通过。
- 代价: 覆写挂在 `Intl.DateTimeFormat.prototype` 上, 任何依赖该方法原始行为的页面脚本
  都会看到被改写后的 `timeZone`; 这是注入本身的固有副作用, 不是本次修复引入的。
