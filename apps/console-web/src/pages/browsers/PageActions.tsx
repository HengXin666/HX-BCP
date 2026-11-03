import type { JSX } from "react";
import { ChevronDown, FileArchive, Folder, FolderOpen, FolderPen, FolderPlus, FolderX, Import, Plus } from "lucide-react";
import type { ImportKind } from "./ImportDialog";
import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@hx/ui";

/**
 * 浏览器页页头的主操作, 只在人类组出现: 主按钮新建浏览器; 导入 (现有 Firefox profile 目录, 账号包 JSON)
 * 与文件夹相关操作各收进一个下拉菜单, 窄屏只占三个按钮宽.
 */
export function PageActions(props: {
	folder: string | null;
	onCreate: () => void;
	onNewFolder: () => void;
	onRenameFolder: (folder: string) => void;
	onRemoveFolder: (folder: string) => void;
	onImport: (kind: ImportKind) => void;
}): JSX.Element {
	const { folder } = props;
	return (
		<span className="flex items-center gap-2">
			<Button size="sm" variant="primary" onClick={props.onCreate} aria-label="新建浏览器" title="新建浏览器">
				<Plus className="size-4" />
				<span className="hidden sm:inline">新建浏览器</span>
			</Button>
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<Button size="sm" variant="outline" aria-label="导入" data-import-menu="">
						<Import className="size-4 sm:hidden" />
						<span className="hidden sm:inline">导入</span>
						<ChevronDown className="size-4" />
					</Button>
				</DropdownMenuTrigger>
				<DropdownMenuContent align="end">
					<DropdownMenuItem onSelect={() => props.onImport("profile")} data-import="profile"><FolderOpen className="size-4" />导入 profile 目录</DropdownMenuItem>
					<DropdownMenuItem onSelect={() => props.onImport("pack")} data-import="pack"><FileArchive className="size-4" />导入账号包</DropdownMenuItem>
				</DropdownMenuContent>
			</DropdownMenu>
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<Button size="sm" variant="outline" aria-label="文件夹操作">
						<Folder className="size-4 sm:hidden" />
						<span className="hidden sm:inline">文件夹</span>
						<ChevronDown className="size-4" />
					</Button>
				</DropdownMenuTrigger>
				<DropdownMenuContent align="end">
					<DropdownMenuItem onSelect={props.onNewFolder}><FolderPlus className="size-4" />新建文件夹</DropdownMenuItem>
					{folder !== null ? (
						<DropdownMenuItem onSelect={() => props.onRenameFolder(folder)}><FolderPen className="size-4" />文件夹改名</DropdownMenuItem>
					) : null}
					{folder !== null ? (
						<DropdownMenuItem onSelect={() => props.onRemoveFolder(folder)}><FolderX className="size-4" />删除文件夹</DropdownMenuItem>
					) : null}
				</DropdownMenuContent>
			</DropdownMenu>
		</span>
	);
}
