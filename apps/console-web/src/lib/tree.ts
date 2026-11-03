/** 把浏览器与文件夹组装成左侧树: 人类组 > 文件夹; API 组 > 项目 > 文件夹. 分组键规则引用 packages/lane 的 group_key. */
import type { TreeNode } from "@hx/ui";
import { Bot, Folder, FolderOpen, Globe, Users } from "lucide-react";
import type { Env, FolderMap } from "./api";

export const HUMAN_KEY = "human";
export const AGENT_PREFIX = "agent:";

/** 树节点 id 对应的范围: 选中哪个节点, 右侧表格就列哪些浏览器. */
export type Scope =
	| { kind: "all" }
	| { kind: "human"; folder: string | null }
	| { kind: "agent"; project: string | null; folder: string | null };

/** 节点 id: all (两组全部), h, h/ (人类组根目录, 即未分类), h/<folder>, a, a/<project>, a/<project>/<folder>. 文件夹与项目名都不含斜杠 (后端校验). */
export function scopeOf(id: string): Scope {
	if (id === "all") return { kind: "all" };
	const [root, a, b] = id.split("/");
	if (root === "a") return { kind: "agent", project: a ?? null, folder: b ?? null };
	return { kind: "human", folder: a ?? null };
}

/** 浏览器是否落在范围内; folder 为 null 表示整组, 为空串的文件夹即分组根目录. */
export function inScope(env: Env, scope: Scope): boolean {
	if (scope.kind === "all") return true;
	if (scope.kind === "human") {
		return env.lane === "human" && (scope.folder === null || env.folder === scope.folder);
	}
	if (env.lane !== "agent") return false;
	if (scope.project !== null && env.project !== scope.project) return false;
	return scope.folder === null || env.folder === scope.folder;
}

/** 范围的面包屑文字, 从组到文件夹. */
export function crumbs(scope: Scope): string[] {
	if (scope.kind === "all") return ["全部浏览器"];
	if (scope.kind === "human") return ["人类组", ...(scope.folder === "" ? ["未分类 (根目录)"] : scope.folder ? [scope.folder] : [])];
	return ["API 组", ...(scope.project ? [scope.project] : []), ...(scope.folder ? [scope.folder] : [])];
}

/** 一组浏览器里是否有被 api 或 mcp 占用的, 用作树节点的状态点. 与表格 "API 占用" 同一个琥珀脉冲点 (pending-input). */
function busy(envs: Env[]): TreeNode["status"] {
	return envs.some((e) => e.in_use) ? "pending-input" : "idle";
}

/** 占用数文本角标, 挂在 API 组与项目节点上, 折叠时也能看到; 没有占用返回 undefined. */
export function busyBadge(envs: Env[]): TreeNode["badge"] {
	const n = envs.filter((e) => e.in_use).length;
	return n > 0 ? { text: "占用 " + n, variant: "text" } : undefined;
}

/** 一个分组下的文件夹节点. */
function folderNodes(prefix: string, names: string[], envs: Env[]): TreeNode[] {
	return names.map((name) => {
		const inside = envs.filter((e) => e.folder === name);
		return { id: prefix + "/" + name, label: name, icon: Folder, meta: String(inside.length), status: busy(inside) };
	});
}

/** 人类组根目录 (没放进任何文件夹) 的节点, 计数后带占比, 一眼看出 "大多数没分类". */
function rootNode(human: Env[]): TreeNode {
	const n = human.filter((e) => e.folder === "").length;
	const pct = human.length ? Math.round((n * 100) / human.length) : 0;
	return { id: "h/", label: "未分类 (根目录)", icon: FolderOpen, meta: n + " · " + pct + "%", status: busy(human.filter((e) => e.folder === "")) };
}

/** API 组下全部项目名: 来自文件夹表与浏览器两处, 去重排序. */
function projectsOf(envs: Env[], folders: FolderMap): string[] {
	const fromFolders = Object.keys(folders)
		.filter((k) => k.startsWith(AGENT_PREFIX))
		.map((k) => k.slice(AGENT_PREFIX.length));
	const fromEnvs = envs.filter((e) => e.lane === "agent").map((e) => e.project);
	return [...new Set([...fromFolders, ...fromEnvs])].filter(Boolean).sort();
}

/** 按名称过滤树: 命中节点保留全部祖先, 命中的分组保留全部子节点. 空查询原样返回. */
export function filterTree(nodes: readonly TreeNode[], query: string): TreeNode[] {
	const q = query.trim().toLowerCase();
	if (!q) return [...nodes];
	const out: TreeNode[] = [];
	for (const n of nodes) {
		if (String(n.label).toLowerCase().includes(q)) {
			out.push(n);
			continue;
		}
		const kids = filterTree(n.children ?? [], q);
		if (kids.length) out.push({ ...n, children: kids });
	}
	return out;
}

/** 树里全部有子节点的 id, 用于全部展开. */
export function branchIds(nodes: readonly TreeNode[]): string[] {
	return nodes.flatMap((n) => (n.children?.length ? [n.id, ...branchIds(n.children)] : []));
}

/** 组装整棵树. */
export function buildTree(envs: Env[], folders: FolderMap): TreeNode[] {
	const human = envs.filter((e) => e.lane === "human");
	const agent = envs.filter((e) => e.lane === "agent");
	const projects = projectsOf(envs, folders).map((project): TreeNode => {
		const mine = agent.filter((e) => e.project === project);
		return {
			id: "a/" + project,
			label: project,
			icon: Globe,
			meta: String(mine.length),
			status: busy(mine),
			badge: busyBadge(mine),
			children: folderNodes("a/" + project, folders[AGENT_PREFIX + project] ?? [], mine),
		};
	});
	return [
		{
			id: "h",
			label: "人类组",
			icon: Users,
			meta: String(human.length),
			children: [rootNode(human), ...folderNodes("h", folders[HUMAN_KEY] ?? [], human)],
		},
		{
			id: "a",
			label: "API 组",
			icon: Bot,
			meta: String(agent.length),
			status: busy(agent),
			badge: busyBadge(agent),
			children: projects,
		},
	];
}
