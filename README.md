# ⚡ Filtrite

Filtrite generates optimized filter lists for [Bromite](https://www.bromite.org/) and [Cromite](https://www.cromite.org/). Learn more about [Custom Ad Block Filters](https://www.bromite.org/custom-filters).

## 📦 Available Lists

Pick a list below, then **tap and hold** to copy the link. In Bromite/Cromite, navigate to **Settings > AdBlock settings** and paste the link into the **Filters URL** field.

| List | Description | Link |
| :--- | :--- | :--- |
| **Adblock Default** | AdGuard + EasyList (Ads, Privacy, Annoyance) | [Download `.dat`](https://github.com/anT0ny54/filtrite/releases/latest/download/adblock.dat) |

👉 [Browse forks for more lists](https://filterlists.010.one/) | [View Sources](https://raw.githubusercontent.com/anT0ny54/Legacy-bromite-adblocklist/refs/heads/main/sources.txt)

*Lists are updated automatically via GitHub Actions.*

---

## 🧩 Filter Syntax Support

`filters.txt` targets the **legacy** Bromite/Cromite ad-blocking path — Chromium's [`subresource_filter`](https://github.com/chromium/chromium/tree/master/components/subresource_filter), converted with `ruleset_converter --input_format=filter-list`. That engine understands a smaller rule set than uBlock Origin/AdGuard/modern Adblock Plus, so every source rule is validated and either kept as-is or rejected outright — never silently reinterpreted into something the converter might mis-parse.

**Kept:**
- `||host^` / `||host/path...` network rules and their `@@` exceptions
- `|http://...` / `|https://...` fully-anchored rules
- Hosts-file entries (`0.0.0.0 host`, `127.0.0.1 host`, `::1 host`)
- The `$` options the real engine's filter-list parser accepts without flagging them deprecated/unsupported/whitelist-only: `third-party` / `~third-party`, `match-case`, and `domain=a.com|~b.com`

**Rejected:** cosmetic filters (`##`, `#@#`, ...), scriptlets/procedural selectors (`+js(...)`, `:has-text(...)`, ...), regex rules (`/.../`) , and any other `$` option (`$script`, `$image`, `$document`, `$sitekey`, `$collapse`, ...) — these either have no effect in this engine or aren't parsed by it at all.

Every unconditional `||host^` block is additionally suffixed with `$third-party` in the final output. This mirrors [Chromium's own documented fix](https://github.com/chromium/chromium/blob/master/components/subresource_filter/FILTER_LIST_GENERATION.md) (`crbug.com/448915986`) for generating filter lists for this engine: without it, a bare host block also matches the main-frame navigation when that host is visited directly as a first-party page, which can make the page look broken instead of just blocking it as a third-party embed elsewhere.

Rejected lines aren't just dropped silently — each build writes a `rejected-*.txt` report (source, line number, reason, original line) alongside the generated `filters.txt`.

---

## 🌐 My Free DNS Server

Experience high-performance filtering with HaGeZi Blocklists (Multi Pro + TIF) via **My Free DNS**.

| Configuration | DNS-over-HTTPS (DoH) Endpoint |
| :--- | :--- |
| **Multi Pro + TIF** | `https://freedns.koyeb.app/dns-query` (Recommended) |
| **Multi Pro + TIF** | `https://freedns-six.vercel.app/api/doh/dns-query` (Recommended) |
| **Multi Pro + TIF** | `https://dnssix.netlify.app/api/doh/dns-query` |

---

## 🛠️ Advanced Blocking

While the built-in blocker is powerful, user scripts take it to the next level—especially for eliminating stubborn cookie banners. Check out my [custom Bromite user scripts repository](https://github.com/xarantolus/bromite-userscripts/).

### Create Your Own Filter Lists
1. **Fork** this repository.
2. **Enable** GitHub Actions in your fork.
3. **Add** a `.txt` file in the `lists/` directory (e.g., `my-list.txt`).
4. **Populate** the file with one filter list URL per line.
5. **Commit and push** your changes.
6. **Grab** your custom link from the `releases/latest/download/...` path.

**Pro Tips:**
- Keep generated files under **20 MB**.
- If the file is too large, trim your sources and rebuild.
- GitHub disables scheduled workflows after 60 days of inactivity; make an occasional commit to keep your fork active.

---

## 🚀 Bandwidth Hero Server

A lightweight image proxy designed to slash bandwidth usage and accelerate your browsing experience. 

Bandwidth Hero Server fetches remote images, compresses them on the fly, and delivers optimized versions to your device for faster loading and lower data consumption.

🖥️ **Try it out:** [Bandwidth Hero](https://bhserv.netlify.app/)

---

## 📄 License & Support

**License:** Free software. Do whatever you want with it. See the [LICENSE](LICENSE) file for details.

**Support the Project:**
If you find this tool useful, consider donating:
- **Bitcoin:** `1HntwKxyqGCfnSGvGLMUTRAqLnTvLarAQP`
  
