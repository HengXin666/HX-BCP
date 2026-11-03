/**
 * Hash 路由: #/<page>/<sub>?k=v. 选 hash 是因为产物用 base "./", 直连 8325 与经 HX-WebX 的
 * /proxy/hx-browser/ 打开都不需要后端回退路由, 刷新也不会 404.
 */
import { useCallback, useEffect, useState } from "react";

export type Route = { path: string[]; query: URLSearchParams };

/** 解析当前 location.hash, 空 hash 视为默认页. */
export function parseHash(hash: string, fallback: string[]): Route {
	const raw = hash.replace(/^#\/?/, "");
	const [pathPart, queryPart] = raw.split("?");
	const path = (pathPart ?? "").split("/").filter(Boolean);
	return { path: path.length ? path : fallback, query: new URLSearchParams(queryPart ?? "") };
}

/** 拼 hash, 空值的 query 不写入, 保持地址短. */
export function formatHash(path: string[], query?: Record<string, string>): string {
	const q = new URLSearchParams();
	for (const [k, v] of Object.entries(query ?? {})) if (v !== "") q.set(k, v);
	const tail = q.toString();
	return "#/" + path.join("/") + (tail ? "?" + tail : "");
}

/** 订阅 hashchange 的当前路由, go 用 replace 写 query (筛选不进历史), 换页用 push. */
export function useRoute(fallback: string[]) {
	const [route, setRoute] = useState<Route>(() => parseHash(window.location.hash, fallback));
	const key = fallback.join("/");

	useEffect(() => {
		const on = (): void => setRoute(parseHash(window.location.hash, key.split("/")));
		window.addEventListener("hashchange", on);
		return () => window.removeEventListener("hashchange", on);
	}, [key]);

	const go = useCallback((path: string[], query?: Record<string, string>, replace = false): void => {
		const next = formatHash(path, query);
		if (next === window.location.hash) return;
		if (replace) {
			window.history.replaceState(null, "", next);
			setRoute(parseHash(next, path));
		} else {
			window.location.hash = next;
		}
	}, []);

	return { route, go };
}
