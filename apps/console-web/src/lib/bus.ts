/**
 * 跨页命令: 命令面板与总览页发出 "新建浏览器" "打开某个环境" 这类意图, 由对应页面执行.
 * 目标页还没挂载时先排队, 页面挂载订阅时取走属于它的那些.
 */
export type Command =
	| { page: "browsers"; kind: "new-browser" }
	| { page: "browsers"; kind: "new-folder" }
	| { page: "browsers"; kind: "import"; what: "profile" | "pack" }
	| { page: "browsers"; kind: "open-env"; id: string }
	| { page: "proxies"; kind: "new-proxy" }
	| { page: "proxies"; kind: "bulk-proxy" };

type Page = Command["page"];
const queue: Command[] = [];
const subs = new Map<Page, Set<(c: Command) => void>>();

/** 发出命令: 有订阅者直接交付, 否则排队. */
export function emit(c: Command): void {
	const set = subs.get(c.page);
	if (set && set.size) for (const f of set) f(c);
	else queue.push(c);
}

/** 订阅某页的命令, 立即交付排队中的同页命令; 返回注销函数. */
export function listen(page: Page, f: (c: Command) => void): () => void {
	const set = subs.get(page) ?? new Set<(c: Command) => void>();
	set.add(f);
	subs.set(page, set);
	for (let i = queue.length - 1; i >= 0; i -= 1) {
		if (queue[i].page === page) {
			const [c] = queue.splice(i, 1);
			setTimeout(() => f(c), 0);
		}
	}
	return () => void set.delete(f);
}
