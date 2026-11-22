/** hash 路由: #/<page>?k=v. 抓包条件写进地址栏, 刷新与分享保持同一视图. */
import { useCallback, useEffect, useState } from "react";

export interface Route {
	page: string;
	query: URLSearchParams;
}

function parse(): Route {
	const raw = window.location.hash.replace(/^#\/?/, "");
	const [page, q] = raw.split("?");
	return { page: page || "overview", query: new URLSearchParams(q ?? "") };
}

export function useRoute(): { route: Route; go: (page: string, query?: Record<string, string>, replace?: boolean) => void } {
	const [route, setRoute] = useState(parse);
	useEffect(() => {
		const on = (): void => setRoute(parse());
		window.addEventListener("hashchange", on);
		return () => window.removeEventListener("hashchange", on);
	}, []);
	const go = useCallback((page: string, query: Record<string, string> = {}, replace = false) => {
		const sp = new URLSearchParams();
		for (const [k, v] of Object.entries(query)) if (v) sp.set(k, v);
		const hash = "#/" + page + (sp.toString() ? "?" + sp.toString() : "");
		if (replace) {
			window.history.replaceState(null, "", hash);
			setRoute(parse());
		} else window.location.hash = hash;
	}, []);
	return { route, go };
}
