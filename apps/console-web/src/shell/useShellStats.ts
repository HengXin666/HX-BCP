/** 外壳用的轻量汇总: 服务状态 (连接指示与在跑数), 最近删除与孤儿进程个数 (侧栏角标). 各自低频轮询. */
import { serviceStatus } from "../lib/api";
import { opsApi } from "../lib/api-ops";
import { systemApi } from "../lib/api-res";
import { usePoll } from "../lib/hooks";

export function useShellStats() {
	const svc = usePoll(serviceStatus, 10000);
	const trash = usePoll(opsApi.trash, 30000);
	const orphans = usePoll(systemApi.orphans, 30000);
	return {
		svc: svc.result,
		running: svc.result?.ok ? svc.result.data.running : null,
		trash: trash.result?.ok ? trash.result.data.length : 0,
		orphans: orphans.result?.ok ? orphans.result.data.count : 0,
	};
}

export type ShellStats = ReturnType<typeof useShellStats>;
