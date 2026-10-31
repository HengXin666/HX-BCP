import { Alert, AlertDescription, AlertTitle, Spin } from "@hx/ui";
import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { CASE_NO, CASE_PR, loadCase, loadIndex } from "../lib/data.ts";
import { compareCases } from "../lib/diff.ts";
import { type CaseIndex, type CaseRecord, previousNo } from "../lib/record.ts";

export type CaseLoad = {
	index: CaseIndex;
	current: CaseRecord;
	previous: CaseRecord | null;
	diff: ReturnType<typeof compareCases>;
	selected: number;
	select: (no: number) => void;
};

const Context = createContext<CaseLoad | null>(null);

/** 所有页面共用的数据; 换一次测评就重新取数并重算对比 */
export function CaseProvider({ children }: { children: ReactNode }) {
	const [selected, setSelected] = useState(CASE_NO);
	const [data, setData] = useState<{ index: CaseIndex; current: CaseRecord; previous: CaseRecord | null } | null>(null);
	const [error, setError] = useState("");
	useEffect(() => {
		setData(null);
		setError("");
		loadIndex(CASE_PR)
			.then(async (index) => {
				const current = await loadCase(CASE_PR, selected);
				const prev = previousNo(index, selected);
				const previous = prev === null ? null : await loadCase(CASE_PR, prev);
				setData({ index, current, previous });
			})
			.catch((reason: unknown) => setError(reason instanceof Error ? reason.message : String(reason)));
	}, [selected]);
	if (error) {
		return (
			<Alert variant="danger">
				<AlertTitle>数据没取到, 不显示任何分数</AlertTitle>
				<AlertDescription>{error}</AlertDescription>
			</Alert>
		);
	}
	if (!data) return <Spin />;
	const load: CaseLoad = { ...data, diff: compareCases(data.current, data.previous), selected, select: setSelected };
	return <Context.Provider value={load}>{children}</Context.Provider>;
}

/** 取当前页面的数据; 不在 Provider 内时直接报错, 不渲染半张空页面 */
export function useCase(): CaseLoad {
	const value = useContext(Context);
	if (!value) throw new Error("useCase 必须在 CaseProvider 内使用");
	return value;
}
