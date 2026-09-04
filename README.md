## filtrite

filtrite builds filter lists for [Bromite](https://www.bromite.org/) and [Cromite](https://www.cromite.org/). Learn more about [Custom Ad Block Filters](https://www.bromite.org/custom-filters).

# Lists

Pick a list below, then tap and hold to copy its link. In Bromite, go to **Settings > AdBlock settings** and paste it into **Filters URL**.

| Link | Description |
| --- | --- |
| [Adblock Default](https://github.com/anT0ny54/filtrite/releases/latest/download/adblock.dat) | AdGuard + EasyList + HaGeZi (Ads, Privacy, Annoyance, Threat Intelligence) [Sources](https://raw.githubusercontent.com/anT0ny54/Legacy-bromite-adblocklist/refs/heads/main/sources.txt) |

More lists: [browse forks](https://filterlists.010.one/).

Updated automatically with GitHub Actions.

**Note:** Some formats may still fail in the generator. If you notice one, open an issue :)

#### :department_store: **My Free DNS Server — free** <a name="dns-server"></a>

Use HaGeZi Blocklists Multi Pro + TIF with [My Free DNS].

| Hagezi Blocklists | DNS-over-HTTPS |
| --- | --- |
| Multi Pro + TIF | `https://freedns-six.vercel.app/api/doh/dns-query` (Recommended) |
| Multi Pro + TIF | `https://dnssix.netlify.app/api/doh/dns-query` |

### Advanced blocking

Bromite’s built-in blocker is good. User scripts make it better — especially for things like cookie banners. See my [custom Bromite user scripts repository](https://github.com/xarantolus/bromite-userscripts/).

### Using your own filter lists

1. Fork the repo.
2. Enable GitHub Actions.
3. Add a `.txt` file in `lists/` like `example-list.txt`.
4. Add one filter list URL per line.
5. Commit and push.
6. Copy the `releases/latest/download/...` link from the release.
7. Keep the generated file under 20 MB.
8. Trim sources if needed, then rebuild.

GitHub disables scheduled workflows after 60 days, so an occasional commit keeps your fork alive.

# ⚡ Bandwidth Hero Server

> A lightweight image proxy that cuts bandwidth and speeds up browsing.

Bandwidth Hero Server fetches remote images, compresses them, and returns optimized versions for faster loading and lower data use.

🖥️ Try [Bandwidth Hero](https://bhserv.netlify.app/).

### [License](LICENSE)

Free software. Do what you want with it.

## Supporting My Project

If you'd like to support the project, donate:

- Bitcoin: `1HntwKxyqGCfnSGvGLMUTRAqLnTvLarAQP`
