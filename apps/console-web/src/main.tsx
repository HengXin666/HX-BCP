import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { ConfirmDialogHost, Toaster } from "@hx/ui";
import "./styles.css";
import { App } from "./App";

const host = document.getElementById("root");
if (!host) throw new Error("缺少 #root 挂载点");
createRoot(host).render(
	<StrictMode>
		<App />
		<ConfirmDialogHost />
		<Toaster />
	</StrictMode>,
);
