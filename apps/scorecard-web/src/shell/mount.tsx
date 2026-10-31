import { StrictMode, type ComponentType } from "react";
import { createRoot } from "react-dom/client";
import "./boot.ts";
import { CaseProvider } from "./CaseContext.tsx";

/** 每个页面的入口: 挂上数据上下文, 页面组件自己从 useCase 取值 */
export function bootPage(Page: ComponentType): void {
	const host = document.getElementById("root");
	if (!host) throw new Error("缺少 #root 挂载点");
	createRoot(host).render(
		<StrictMode>
			<CaseProvider>
				<Page />
			</CaseProvider>
		</StrictMode>,
	);
}
