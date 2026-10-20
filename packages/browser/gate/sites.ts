export type SiteMeta = {
  id: string;
  name: string;
  url: string;
  role: string;
  measures: string;
  reads: string;
  passes: string;
};

/** What each public checker is for. Scores come from the page, not from this list. */
export const SITES: SiteMeta[] = [
  {
    id: "creepjs",
    name: "CreepJS",
    url: "https://abrahamjuliot.github.io/creepjs/",
    role: "找谎",
    measures: "这个站找浏览器有没有说谎：函数被改过没有、是不是没开窗口的无头浏览器、有没有装专门躲检测的补丁。",
    reads: "页面上的三句百分比。headless 是「我认定你是无头」。like headless 是「像无头」。stealth 是「躲检测的补丁」。分 = 100 − headless%。",
    passes: "headless 和 like headless 都低于 50%。100% headless 就是失败。",
  },
  {
    id: "sannysoft",
    name: "Sannysoft",
    url: "https://bot.sannysoft.com/",
    role: "自动化痕迹表",
    measures: "一张对照表，逐条看浏览器像不像被脚本控制。常见项是 WebDriver、插件列表、Chrome 对象、语言、显卡名字。",
    reads: "表里标了 passed 和 failed 的行数。另外看 WebDriver 在不在，以及 UA 里有没有 HeadlessChrome。",
    passes: "失败行是 0，并且没有 WebDriver。有失败行，这一站就是失败。",
  },
  {
    id: "iphey",
    name: "Iphey",
    url: "https://iphey.com/",
    role: "像不像真人",
    measures: "问这套浏览器身份像不像真人用过的。它给一个词（Unreliable 或 Trustworthy）和一个 MX 分，0 到 100，越高越像。",
    reads: "页面上的 Unreliable / Trustworthy，以及旁边的 MX SCORE。单词 Reliable 出现在 Unreliable 里面时，仍算 Unreliable。",
    passes: "结论不是 Unreliable。MX 分就是这一站的分数。",
  },
  {
    id: "browserscan",
    name: "BrowserScan",
    url: "https://www.browserscan.net/",
    role: "是不是机器人",
    measures: "两件事一起看：是不是机器人，以及指纹像不像真浏览器。真实度高，不代表它没把你标成机器人。",
    reads: "Bot Detection 是 Yes 还是 No。fingerprint authenticity 是真实度百分比。",
    passes: "Bot Detection 不是 Yes。标成机器人就失败，真实度只是附带的数字。",
  },
  {
    id: "pixelscan",
    name: "Pixelscan",
    url: "https://pixelscan.net/fingerprint-check",
    role: "硬件和代理是否打架",
    measures: "看显卡、时区、语言、代理是不是同一台机器、同一个出口。博客文章里的 inconsistent 不算结果。",
    reads: "扫描跑完之后的 Consistent 或 Inconsistent。停在首页或文章列表，就是没测到。",
    passes: "结果是 Consistent。没扫完不打分，门禁也不放行。",
  },
  {
    id: "deviceinfo",
    name: "Device & Browser Info",
    url: "https://deviceandbrowserinfo.com/are_you_a_bot",
    role: "直接回答是不是机器人",
    measures: "页面标题就是问题：你是不是机器人。它会列出理由，例如 webdriver 是真的、连接了 CDP。",
    reads: "You are a bot，或者它明确说你不是机器人。",
    passes: "它说你不是机器人。说你是，这一站就是 0 分。",
  },
  {
    id: "browserleaks-webrtc",
    name: "BrowserLeaks WebRTC",
    url: "https://browserleaks.com/webrtc",
    role: "内网地址有没有漏",
    measures: "网页能不能看到电脑的内网地址。指纹浏览器应该只露出代理的公网地址，不露出局域网。",
    reads: "页面上的 No Leak，以及 Public IP Address。",
    passes: "写着 No Leak。出现本机局域网地址就是失败。",
  },
  {
    id: "browserleaks-canvas",
    name: "BrowserLeaks Canvas",
    url: "https://browserleaks.com/canvas",
    role: "只记录，不打分",
    measures: "同一张图，不同显卡画出来会有一点点差别。这个站把差别收成一个签名。它不说你是不是机器人。",
    reads: "Signature（签名）和 Uniqueness（有多少人跟你撞签名）。",
    passes: "一次运行不算过也不算不过。同一个环境开两次，签名该一样；换一个环境，签名该不一样。",
  },
  {
    id: "clearcote",
    name: "Clearcote",
    url: "https://www.clearcotelabs.com/audit",
    role: "各项信号是否互相打架",
    measures: "不看单项漂不漂亮，看浏览器各处说的是不是同一件事。比如系统、显卡、时区、语言彼此矛盾，就会被记下来。",
    reads: "Coherence score（连贯分，满分 100），以及多少项一致。必须点过 Run the audit，停在说明页不算。",
    passes: "页面写 Coherent。有少数不一致时，仍以它自己的这个词为准。",
  },
];
