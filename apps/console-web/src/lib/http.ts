/**
 * 统一请求层: 超时, 幂等重试, 统一错误对象, 请求取消. api.ts / api-res.ts / api-write.ts / session.ts 都走这里,
 * 不再各自写 fetch 与 try/catch. 判据与后端信封一致 (code 0 为成功), 失败一律落成 Result 而不抛异常.
 * 本文件的 url 参数都是完整相对地址 (含 API 前缀), 与旧 getEnvelope 的约定一致.
 */

/**
 * 8325 的接口前缀, 相对页面所在目录解析: 直连是 /api/v1, 经 HX-WebX 打开是 /proxy/hx-browser/api/v1.
 * 不能写成以 / 开头, 否则子路径下会打到面板根.
 */
export const API = "api/v1";

/** 每个请求都声明人类通道; 身份凭证是 8325 设的 HttpOnly 会话 cookie 或 dev 代理注入的令牌, 脚本看不到. */
export const HUMAN_HEADERS = { Accept: "application/json", "x-bcp-lane": "human" } as const;

/** 统一结果: 服务未启动或业务失败时返回 ok=false 而不是抛异常, 页面显示"未连接". */
export type Result<T> = { ok: true; data: T } | { ok: false; error: string };

export type Envelope<T> = { code?: number | string; msg?: string; data?: T };

/** 统一错误对象: kind 分类便于页面区分 "未连接" (network/timeout/abort) 与业务失败 (http/envelope). */
export type HttpErrorKind = "network" | "timeout" | "abort" | "http" | "envelope";
export type HttpError = { kind: HttpErrorKind; message: string; status?: number; code?: number | string };

/** 把统一错误压成一行文案, 兼容旧代码里直接读 error 字符串的地方. */
export function errorLine(e: HttpError): string {
	if (e.kind === "http") return "HTTP " + (e.status ?? "") + (e.code !== undefined ? " " + String(e.code) : "") + (e.message ? " " + e.message : "");
	return e.message;
}

export type HttpResult<T> = { ok: true; data: T } | { ok: false; error: HttpError };

export type RequestOpts = {
	method?: string;
	/** JSON 请求体, undefined 不发 body. */
	body?: unknown;
	/** 外部取消信号 (如组件卸载); 与内部超时信号合并. */
	signal?: AbortSignal;
	/** 单次请求超时毫秒, 缺省 15000. */
	timeoutMs?: number;
	/** 失败重试次数 (不含首次), 缺省 0. 只对读请求与显式幂等的写请求开. */
	retries?: number;
	/** 额外请求头. */
	headers?: Record<string, string>;
};

const DEFAULT_TIMEOUT = 15000;

/** 把 catch 到的 unknown 收成一行文案, 不做 as Error 断言. */
function messageOf(err: unknown): string {
	return err instanceof Error ? err.message : String(err);
}

/** 合并外部取消信号与超时信号, 任一触发即中止. 返回信号与清理函数 (清掉定时器并断开监听). */
function withTimeout(external: AbortSignal | undefined, timeoutMs: number): { signal: AbortSignal; done: () => void; timedOut: () => boolean } {
	const ctl = new AbortController();
	let timed = false;
	const timer = setTimeout(() => {
		timed = true;
		ctl.abort();
	}, timeoutMs);
	const onExternal = (): void => ctl.abort();
	if (external) {
		if (external.aborted) ctl.abort();
		else external.addEventListener("abort", onExternal, { once: true });
	}
	return {
		signal: ctl.signal,
		timedOut: () => timed,
		done: () => {
			clearTimeout(timer);
			if (external) external.removeEventListener("abort", onExternal);
		},
	};
}

const sleep = (ms: number): Promise<void> => new Promise((r) => setTimeout(r, ms));

/** 5xx 与网络/超时可重试; 4xx 是请求本身的问题, 不重试. */
function retryable(e: HttpError): boolean {
	if (e.kind === "network" || e.kind === "timeout") return true;
	if (e.kind === "http" && e.status !== undefined) return e.status >= 500;
	return false;
}

/** 发一次原始请求, 只落 HTTP 层结果; 不解析信封. 外部 abort 与超时分别归类为 abort 与 timeout. */
async function once(url: string, opts: RequestOpts): Promise<HttpResult<Response>> {
	const t = withTimeout(opts.signal, opts.timeoutMs ?? DEFAULT_TIMEOUT);
	try {
		const res = await fetch(url, {
			method: opts.method ?? "GET",
			headers: {
				...HUMAN_HEADERS,
				...(opts.body === undefined ? {} : { "Content-Type": "application/json" }),
				...(opts.headers ?? {}),
			},
			body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
			signal: t.signal,
		});
		return { ok: true, data: res };
	} catch (err) {
		if (t.timedOut()) return { ok: false, error: { kind: "timeout", message: "请求超时" } };
		if (opts.signal?.aborted) return { ok: false, error: { kind: "abort", message: "请求已取消" } };
		return { ok: false, error: { kind: "network", message: messageOf(err) } };
	} finally {
		t.done();
	}
}

/** 带重试地发请求到拿回 Response (或最终失败). abort 不重试. 退避 200ms 起翻倍, 上限 2s. */
async function fetchWithRetry(url: string, opts: RequestOpts): Promise<HttpResult<Response>> {
	const max = Math.max(0, opts.retries ?? 0);
	let last: HttpResult<Response> = { ok: false, error: { kind: "network", message: "未发出" } };
	for (let attempt = 0; attempt <= max; attempt += 1) {
		last = await once(url, opts);
		if (last.ok) {
			if (last.data.ok || last.data.status < 500 || attempt === max) return last;
			// 5xx: 转成错误对象以决定是否重试, 下面统一退避
			last = { ok: false, error: { kind: "http", status: last.data.status, message: "" } };
		}
		if (last.error.kind === "abort" || !retryable(last.error) || attempt === max) return last;
		await sleep(Math.min(2000, 200 * 2 ** attempt));
	}
	return last;
}

/** 从失败响应里取后端信封的 code 与 msg, 拼成 http 错误. */
async function httpError(res: Response): Promise<HttpError> {
	try {
		const body = (await res.json()) as Envelope<unknown>;
		if (body.msg) return { kind: "http", status: res.status, code: body.code, message: body.msg };
	} catch {
		// 非 JSON 响应只报状态码
	}
	return { kind: "http", status: res.status, message: "" };
}

/** 读裸 JSON (不含信封), 只处理网络与 HTTP 状态. 缓存统计那类裸 JSON 接口用它. */
export async function getJson<T>(url: string, opts: RequestOpts = {}): Promise<HttpResult<T>> {
	const r = await fetchWithRetry(url, { ...opts, method: opts.method ?? "GET" });
	if (!r.ok) return r;
	if (!r.data.ok) return { ok: false, error: await httpError(r.data) };
	try {
		return { ok: true, data: (await r.data.json()) as T };
	} catch (err) {
		return { ok: false, error: { kind: "network", message: messageOf(err) } };
	}
}

/** 读写信封 {code,msg,data}: code 非 0 落 envelope 错误. 读请求默认重试 2 次, 写请求默认不重试. */
export async function requestEnvelope<T>(url: string, opts: RequestOpts = {}): Promise<HttpResult<T>> {
	const isWrite = (opts.method ?? "GET") !== "GET";
	const retries = opts.retries ?? (isWrite ? 0 : 2);
	const r = await fetchWithRetry(url, { ...opts, retries });
	if (!r.ok) return r;
	if (!r.data.ok) return { ok: false, error: await httpError(r.data) };
	let env: Envelope<T>;
	try {
		env = (await r.data.json()) as Envelope<T>;
	} catch (err) {
		return { ok: false, error: { kind: "network", message: messageOf(err) } };
	}
	if (env.code !== 0) return { ok: false, error: { kind: "envelope", code: env.code, message: env.msg ?? "code " + String(env.code) } };
	return { ok: true, data: env.data as T };
}

/** 兼容垫片: 把 HttpResult 压回旧的 Result<T> (error 为字符串), 让现有页面无需改动. */
export function toResult<T>(r: HttpResult<T>): Result<T> {
	return r.ok ? { ok: true, data: r.data } : { ok: false, error: errorLine(r.error) };
}

/** 读信封并直接返回旧 Result (字符串错误). */
export function getEnvelopeCompat<T>(url: string, signal?: AbortSignal): Promise<Result<T>> {
	return requestEnvelope<T>(url, { signal }).then(toResult);
}

/** 读裸 JSON 并直接返回旧 Result. */
export function getRawCompat<T>(url: string, signal?: AbortSignal): Promise<Result<T>> {
	return getJson<T>(url, { signal }).then(toResult);
}

/** 写请求并直接返回旧 Result. path 不含 API 前缀 (与旧 call / send 一致). idempotent 为真时才重试. */
export function sendCompat<T>(method: string, path: string, body?: unknown, idempotent = false, headers?: Record<string, string>): Promise<Result<T>> {
	return requestEnvelope<T>(API + path, { method, body, headers, retries: idempotent ? 2 : 0 }).then(toResult);
}
