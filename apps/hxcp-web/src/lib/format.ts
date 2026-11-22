/** 字节, 百分比, 时间的展示格式. 一处定义, 各页一致. */
export function bytes(n: number | undefined): string {
	const v = Number(n ?? 0);
	if (v < 1024) return v + " B";
	if (v < 1048576) return (v / 1024).toFixed(1) + " KB";
	if (v < 1073741824) return (v / 1048576).toFixed(2) + " MB";
	return (v / 1073741824).toFixed(2) + " GB";
}

export function pct(x: number | undefined): string {
	return (Number(x ?? 0) * 100).toFixed(1) + "%";
}

const z = (n: number): string => (n < 10 ? "0" : "") + n;

/** 今天只给时分秒, 跨天带月日, 抓包一次看几天时先后关系不乱. */
export function clock(t: number): string {
	const d = new Date(t * 1000);
	const now = new Date();
	const hms = z(d.getHours()) + ":" + z(d.getMinutes()) + ":" + z(d.getSeconds());
	return d.toDateString() === now.toDateString() ? hms : z(d.getMonth() + 1) + "-" + z(d.getDate()) + " " + hms;
}

export function ago(t: number | undefined): string {
	if (!t) return "-";
	const s = Math.max(0, Date.now() / 1000 - t);
	if (s < 60) return Math.round(s) + " 秒前";
	if (s < 3600) return Math.round(s / 60) + " 分钟前";
	if (s < 86400) return Math.round(s / 3600) + " 小时前";
	return Math.round(s / 86400) + " 天前";
}

export function splitUrl(u: string): [string, string] {
	try {
		const x = new URL(u);
		return [x.host, x.pathname + x.search];
	} catch {
		return ["", u];
	}
}

export async function copyText(text: string): Promise<boolean> {
	try {
		await navigator.clipboard.writeText(text);
		return true;
	} catch {
		const ta = document.createElement("textarea");
		ta.value = text;
		document.body.appendChild(ta);
		ta.select();
		const ok = document.execCommand("copy");
		ta.remove();
		return ok;
	}
}
