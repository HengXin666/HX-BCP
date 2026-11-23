// vscode-icons file-type icons for the terminal file panel. The SVGs are
// vendored under /vscode-icons (MIT, see web/public/vscode-icons/README.md).

const folderClosed = "default_folder.svg"
const folderOpened = "default_folder_opened.svg"
const fallback = "default_file.svg"

const extensionMap: Record<string, string> = {
  // Code
  go: "file_type_go.svg",
  ts: "file_type_typescript.svg",
  mts: "file_type_typescript.svg",
  cts: "file_type_typescript.svg",
  tsx: "file_type_reactjs.svg",
  dts: "file_type_typescriptdef.svg",
  js: "file_type_js.svg",
  mjs: "file_type_js.svg",
  cjs: "file_type_js.svg",
  jsx: "file_type_reactjs.svg",
  vue: "file_type_vue.svg",
  py: "file_type_python.svg",
  rs: "file_type_rust.svg",
  c: "file_type_c.svg",
  h: "file_type_c.svg",
  cpp: "file_type_cpp.svg",
  hpp: "file_type_cpp.svg",
  cc: "file_type_cpp.svg",
  java: "file_type_java.svg",
  php: "file_type_php.svg",
  rb: "file_type_ruby.svg",
  // Web
  html: "file_type_html.svg",
  htm: "file_type_html.svg",
  css: "file_type_css.svg",
  scss: "file_type_scss.svg",
  // Data & config
  json: "file_type_json.svg",
  yaml: "file_type_yaml.svg",
  yml: "file_type_yaml.svg",
  toml: "file_type_toml.svg",
  xml: "file_type_xml.svg",
  csv: "file_type_db.svg",
  db: "file_type_db.svg",
  sqlite: "file_type_sqlite.svg",
  sql: "file_type_sql.svg",
  // Docs
  md: "file_type_markdown.svg",
  txt: "file_type_log.svg",
  log: "file_type_log.svg",
  pdf: "file_type_pdf.svg",
  // Containers & scripting
  dockerfile: "file_type_docker.svg",
  sh: "file_type_shell.svg",
  bash: "file_type_shell.svg",
  zsh: "file_type_shell.svg",
  fish: "file_type_shell.svg",
  // Archives & binaries
  zip: "file_type_zip.svg",
  tar: "file_type_zip.svg",
  gz: "file_type_zip.svg",
  tgz: "file_type_zip.svg",
  "7z": "file_type_zip.svg",
  rar: "file_type_zip.svg",
  bin: "file_type_binary.svg",
  exe: "file_type_binary.svg",
  deb: "file_type_binary.svg",
  rpm: "file_type_binary.svg",
  // Media
  png: "file_type_image.svg",
  jpg: "file_type_image.svg",
  jpeg: "file_type_image.svg",
  gif: "file_type_image.svg",
  svg: "file_type_image.svg",
  webp: "file_type_image.svg",
  ico: "file_type_image.svg",
  bmp: "file_type_image.svg",
  mp4: "file_type_video.svg",
  mkv: "file_type_video.svg",
  avi: "file_type_video.svg",
  mov: "file_type_video.svg",
  webm: "file_type_video.svg",
  mp3: "file_type_audio.svg",
  wav: "file_type_audio.svg",
  flac: "file_type_audio.svg",
  ogg: "file_type_audio.svg",
  // Fonts
  ttf: "file_type_font.svg",
  otf: "file_type_font.svg",
  woff: "file_type_font.svg",
  woff2: "file_type_font.svg",
  // Credentials & keys
  pem: "file_type_key.svg",
  key: "file_type_key.svg",
  p12: "file_type_key.svg",
  pfx: "file_type_key.svg",
  // Tooling
  node: "file_type_node.svg",
  npmrc: "file_type_npm.svg",
  gitignore: "file_type_git.svg",
  gitattributes: "file_type_git.svg",
}

/** Extension for a filename; dotfiles like `.env` map by their full name. */
function extensionOf(name: string): string {
  const lower = name.toLowerCase()
  const dot = lower.lastIndexOf(".")
  if (dot <= 0) return lower.startsWith(".") ? lower.slice(1) : ""
  return lower.slice(dot + 1)
}

/** Return the vscode-icons filename for an entry (name + directory flag). */
export function fileIconName(name: string, isDir: boolean, open = false): string {
  if (isDir) return open ? folderOpened : folderClosed
  const extension = extensionOf(name)
  return extensionMap[extension] ?? fallback
}

export function FileIcon({
  name,
  isDir,
  open,
  className,
}: {
  name: string
  isDir: boolean
  open?: boolean
  className?: string
}) {
  const icon = fileIconName(name, isDir, open)
  return <img src={`/vscode-icons/${icon}`} alt="" className={className} draggable={false} />
}
