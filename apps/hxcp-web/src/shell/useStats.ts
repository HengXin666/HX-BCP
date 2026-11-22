import { api, type Stats } from "../lib/api";
import { usePoll, type Poll } from "../lib/hooks";

/** 顶栏与总览共用一份 /stats 轮询, 5 秒一次. */
export function useStats(): Poll<Stats> {
	return usePoll(api.stats, 5000);
}
