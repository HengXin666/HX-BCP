import { useState } from "react"
import { Network, Radio } from "lucide-react"

import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { NodesPage } from "@/pages/nodes-page"
import { SubscriptionsPage } from "@/pages/subscriptions-page"

interface InventoryPageProps {
  onNotice: (message: string, tone?: "success" | "error") => void
  initialView?: "subscriptions" | "nodes"
}

export function InventoryPage({ onNotice, initialView = "subscriptions" }: InventoryPageProps) {
  const [view, setView] = useState(initialView)

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">订阅与节点</h1>
        <p className="mt-1 max-w-3xl text-sm leading-5 text-muted-foreground">
          在同一库存中管理订阅来源、刷新快照、去重节点和质量检测。
        </p>
      </div>

      <Tabs value={view} onValueChange={(value) => setView(value as typeof view)}>
        <TabsList aria-label="订阅与节点视图">
          <TabsTrigger value="subscriptions"><Radio className="mr-1.5 size-3.5" />订阅来源</TabsTrigger>
          <TabsTrigger value="nodes"><Network className="mr-1.5 size-3.5" />节点库存</TabsTrigger>
        </TabsList>
        <TabsContent value="subscriptions"><SubscriptionsPage embedded onNotice={onNotice} /></TabsContent>
        <TabsContent value="nodes"><NodesPage embedded onNotice={onNotice} /></TabsContent>
      </Tabs>
    </div>
  )
}
