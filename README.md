# ⚡ Filtrite

Filtrite generates optimized filter lists for [Bromite](https://www.bromite.org/) and [Cromite](https://www.cromite.org/). Learn more about [Custom Ad Block Filters](https://www.bromite.org/custom-filters).

## 📦 Available Lists

Pick a list below, then **tap and hold** to copy the link. In Bromite/Cromite, navigate to **Settings > AdBlock settings** and paste the link into the **Filters URL** field.

| List | Description | Link |
| :--- | :--- | :--- |
| **Adblock Default** | AdGuard + EasyList + HaGeZi (Ads, Privacy, Annoyance, Threat Intelligence) | [Download `.dat`](https://github.com/anT0ny54/filtrite/releases/latest/download/adblock.dat) |

👉 [Browse forks for more lists](https://filterlists.010.one/) | [View Sources](https://raw.githubusercontent.com/anT0ny54/Legacy-bromite-adblocklist/refs/heads/main/sources.txt)

*Lists are updated automatically via GitHub Actions.*

> [!NOTE]
> Some formats may still encounter errors in the generator. If you spot one, please [open an issue](https://github.com/anT0ny54/filtrite/issues).

---

## 🌐 My Free DNS Server

Experience high-performance filtering with HaGeZi Blocklists (Multi Pro + TIF) via **My Free DNS**.

| Configuration | DNS-over-HTTPS (DoH) Endpoint |
| :--- | :--- |
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
  
