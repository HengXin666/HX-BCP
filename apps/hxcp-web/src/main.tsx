import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { ConfirmDialogHost, Toaster, TooltipProvider } from "@hx/ui";
import "./styles.css";
import { App } from "./App";
import { applyTheme } from "./lib/theme";

const host = document.getElementById("root");
if (!host) throw new Error("缺少 #root 挂载点");
// 挂载前先落主题, 首屏不闪色
applyTheme();
createRoot(host).render(
	<StrictMode>
		<TooltipProvider>
			<App />
			<ConfirmDialogHost />
			<Toaster />
		</TooltipProvider>
	</StrictMode>,
);
