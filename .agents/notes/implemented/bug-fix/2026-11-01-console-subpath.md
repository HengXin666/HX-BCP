# 控制台在子路径下可用 + 静态缓存头

日期: 2026-11-01. 详细证据: .review/css-under-subpath.md.

## 问题

HX-WebX 用 /proxy/hx-browser/ 转发 8325. 新控制台产物里资源与接口都是以 / 开头的绝对路径, 浏览器从子路径打开时解析到面板根 (36000/assets/... 返回面板 html), CSS 与接口都打不到 8325. 旧 bundle 用的是 ./assets, 所以是新构建引入的退化.

## 决定

- vite base: "./". 产物 index.html 引用 ./assets/..., 任意根下都相对页面解析. 不用 HX-WebX 的前缀做 base, 8325 不应知道自己被谁代理.
- 前端接口前缀 API = "api/v1" (相对). 页面没有前端路由, 文档始终在根目录, 相对解析稳定. 直连仍是 /api/v1.
- 会话 cookie Path: 页面在 x-bcp-base 头里报自己的目录, 后端 api_lane.cookie_path() 用 <base>api/v1, 不合法回退 /api/v1. 否则子路径下 Path=/api/v1 的 cookie 不会随 /proxy/hx-browser/api/v1 请求发出. Path 不是安全边界, cookie 仍 HttpOnly + SameSite strict, 同源判定 (lane.same_origin) 不变.
- web_static.ConsoleStatic: index.html 与其他 html 回 no-cache (每次用 etag 重验), assets/ 回 public, max-age=31536000, immutable (vite 文件名带内容哈希). 304 也带同样的头.

## 约束

不改 HX-WebX. 将来若加前端路由 (history 模式), 需要改成从 import.meta.url 推导根, 不能再依赖文档目录.
