import { useState, type JSX } from "react";
import { AppFrame, Badge, Card, CardContent, CardHeader, CardTitle, Separator, Tabs, TabsContent, TabsList, TabsTrigger } from "@hx/ui";
import { EnvironmentsPage } from "./pages/EnvironmentsPage";
import { CacheProxyPage } from "./pages/CacheProxyPage";
import { FingerprintPage } from "./pages/FingerprintPage";
import { LanesPage } from "./pages/LanesPage";

const TABS = [
	{ id: "env", label: "浏览器环境" },
	{ id: "cache", label: "缓存代理" },
	{ id: "fingerprint", label: "指纹评分" },
	{ id: "lanes", label: "通道说明" },
] as const;

/** 控制台外壳: 四个只读视图, 人类操作与 Agent 会话分区. */
export function App(): JSX.Element {
	const [tab, setTab] = useState<string>("env");

	return (
		<AppFrame>
			<Card variant="solid" className="m-3">
				<CardHeader>
					<CardTitle>HX-BCP 控制台</CardTitle>
					<Badge variant="secondary">只读视图</Badge>
				</CardHeader>
				<CardContent>
					<Tabs defaultValue={tab} onValueChange={setTab}>
						<TabsList>
							{TABS.map((item) => (
								<TabsTrigger key={item.id} value={item.id}>{item.label}</TabsTrigger>
							))}
						</TabsList>
						<Separator className="my-3" />
						<TabsContent value="env"><EnvironmentsPage /></TabsContent>
						<TabsContent value="cache"><CacheProxyPage /></TabsContent>
						<TabsContent value="fingerprint"><FingerprintPage /></TabsContent>
						<TabsContent value="lanes"><LanesPage /></TabsContent>
					</Tabs>
				</CardContent>
			</Card>
		</AppFrame>
	);
}
