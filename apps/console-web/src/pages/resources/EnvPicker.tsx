/** 资源绑定或应用到环境时的多选: Combobox 只列人类组可写的环境 (API 组只读, 不出现在候选里). */
import { useMemo, type JSX } from "react";
import { Combobox, type ComboboxOption } from "@hx/ui";
import { listEnvs, type Env } from "../../lib/api";
import { usePoll } from "../../lib/hooks";

/** 人类组可写环境的列表, 资源页共用. */
export function useHumanEnvs(): { envs: Env[]; error: string } {
	const res = usePoll(listEnvs, 15000);
	const envs = useMemo(() => (res.result?.ok ? res.result.data.filter((e) => e.lane === "human" && !e.readonly) : []), [res.result]);
	return { envs, error: res.result && !res.result.ok ? res.result.error : "" };
}

export function EnvPicker(props: { envs: Env[]; value: string[]; onChange: (ids: string[]) => void; disabled?: boolean }): JSX.Element {
	const options: ComboboxOption[] = useMemo(
		() => props.envs.map((e) => ({ value: e.id, label: e.name + (e.folder ? " (" + e.folder + ")" : ""), keywords: [e.id, e.folder, ...(e.tags ?? [])] })),
		[props.envs],
	);
	return (
		<Combobox
			multiple
			options={options}
			value={props.value}
			onValueChange={props.onChange}
			disabled={props.disabled}
			placeholder="选择人类组环境"
			searchPlaceholder="按名称, id, 文件夹, 标签搜索"
			emptyText="没有可写的人类组环境"
			className="w-full"
		/>
	);
}
