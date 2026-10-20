"""国家 -> 时区/语言映射 —— 同一事实的**唯一**定义处 (2026-10-13)。

Agent Note: .agents/notes/implemented/feature/2026-10-13-hxbrowser-visible-window-and-multi-instance.md

## 为什么抽出来

改动前这张表在**两处**各有一份:

    fingerprint.py  _tz_for_country()     随机指纹生成时按 country 选时区
    api.py          _apply_timezone_from_ip()  match_ip 模式按出口 IP 国家对齐时区
                                            —— 它**另带**一份 country->lang 表

两份必然不同步 (本仓栽过 `cached()` 与 `verdict_of()` 的 stale 判据不一致, 闸门据此
跳过探测直接判死整轮)。而两处判的是**同一个事实**: "这个国家的用户, 时区/语言该是什么"。

抽出来的同时把两份表**合并** (取并集), 于是两边行为一致: 原来 api.py 认
"SG" 而 fingerprint.py 不认, 现在都认。

## 语义边界

    tz_for_country  = 国家 -> IANA 时区。**未收录返回空串** (调用方决定回落,
                      不在这里偷偷给个默认值 —— 那会让"没收录"和"收录了"长得一样)
    lang_for_country = 国家 -> BCP-47 首选语言 (带次级标签, 如 "zh-CN,zh")
"""

from __future__ import annotations

COUNTRY_TZ: dict[str, str] = {
    "US": "America/New_York",
    "CA": "America/Toronto",
    "GB": "Europe/London",
    "DE": "Europe/Berlin",
    "FR": "Europe/Paris",
    "NL": "Europe/Amsterdam",
    "JP": "Asia/Tokyo",
    "KR": "Asia/Seoul",
    "SG": "Asia/Singapore",
    "HK": "Asia/Hong_Kong",
    "TW": "Asia/Taipei",
    "CN": "Asia/Shanghai",
    "AU": "Australia/Sydney",
    "BR": "America/Sao_Paulo",
    "ES": "Europe/Madrid",
    "PT": "Europe/Lisbon",
    "IT": "Europe/Rome",
    "IN": "Asia/Kolkata",
}

COUNTRY_LANG: dict[str, str] = {
    "US": "en-US,en",
    "CA": "en-CA,en",
    "GB": "en-GB,en",
    "DE": "de-DE,de",
    "FR": "fr-FR,fr",
    "NL": "nl-NL,nl",
    "JP": "ja-JP,ja",
    "KR": "ko-KR,ko",
    "SG": "en-SG,en",
    "HK": "zh-HK,zh",
    "TW": "zh-TW,zh",
    "CN": "zh-CN,zh",
    "AU": "en-AU,en",
    "BR": "pt-BR,pt",
    "ES": "es-ES,es",
    "PT": "pt-PT,pt",
    "IT": "it-IT,it",
    "IN": "en-IN,en",
}


# 2026-10-16 扩充 (语言跟随出口 IP): 取 nodecloak regional.rs 的国家->首选语言表并集,
# 既有键的取值**不改** (setdefault), 只补新国家。时区取该国人口最多的时区。
_EXTRA = {
    "NZ": ("Pacific/Auckland", "en-NZ,en"),
    "IE": ("Europe/Dublin", "en-IE,en"),
    "AT": ("Europe/Vienna", "de-AT,de,en"),
    "CH": ("Europe/Zurich", "de-CH,de,en"),
    "BE": ("Europe/Brussels", "nl-BE,nl,en"),
    "MX": ("America/Mexico_City", "es-MX,es,en"),
    "AR": ("America/Argentina/Buenos_Aires", "es-AR,es,en"),
    "CL": ("America/Santiago", "es-CL,es,en"),
    "CO": ("America/Bogota", "es-CO,es,en"),
    "PE": ("America/Lima", "es-PE,es,en"),
    "VN": ("Asia/Ho_Chi_Minh", "vi-VN,vi,en"),
    "TH": ("Asia/Bangkok", "th-TH,th,en"),
    "ID": ("Asia/Jakarta", "id-ID,id,en"),
    "MY": ("Asia/Kuala_Lumpur", "ms-MY,ms,en"),
    "PH": ("Asia/Manila", "en-PH,en"),
    "PL": ("Europe/Warsaw", "pl-PL,pl,en"),
    "CZ": ("Europe/Prague", "cs-CZ,cs,en"),
    "SK": ("Europe/Bratislava", "sk-SK,sk,en"),
    "HU": ("Europe/Budapest", "hu-HU,hu,en"),
    "RO": ("Europe/Bucharest", "ro-RO,ro,en"),
    "BG": ("Europe/Sofia", "bg-BG,bg,en"),
    "GR": ("Europe/Athens", "el-GR,el,en"),
    "SE": ("Europe/Stockholm", "sv-SE,sv,en"),
    "NO": ("Europe/Oslo", "nb-NO,nb,en"),
    "DK": ("Europe/Copenhagen", "da-DK,da,en"),
    "FI": ("Europe/Helsinki", "fi-FI,fi,en"),
    "IS": ("Atlantic/Reykjavik", "is-IS,is,en"),
    "EE": ("Europe/Tallinn", "et-EE,et,en"),
    "LT": ("Europe/Vilnius", "lt-LT,lt,en"),
    "LV": ("Europe/Riga", "lv-LV,lv,en"),
    "RU": ("Europe/Moscow", "ru-RU,ru,en"),
    "UA": ("Europe/Kyiv", "uk-UA,uk,en"),
    "TR": ("Europe/Istanbul", "tr-TR,tr,en"),
    "IL": ("Asia/Jerusalem", "he-IL,he,en"),
    "SA": ("Asia/Riyadh", "ar-SA,ar,en"),
    "AE": ("Asia/Dubai", "ar-AE,ar,en"),
    "EG": ("Africa/Cairo", "ar-EG,ar,en"),
    "ZA": ("Africa/Johannesburg", "en-ZA,en"),
}
for _cc, (_tz, _lang) in _EXTRA.items():
    COUNTRY_TZ.setdefault(_cc, _tz)
    COUNTRY_LANG.setdefault(_cc, _lang)


def tz_for_country(country: str) -> str:
    """国家码 -> IANA 时区; 未收录返回空串 (不猜)。"""
    return COUNTRY_TZ.get((country or "").upper(), "")


def lang_for_country(country: str) -> str:
    """国家码 -> 首选语言 (含次级标签); 未收录返回空串 (不猜)。"""
    return COUNTRY_LANG.get((country or "").upper(), "")
