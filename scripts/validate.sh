#!/usr/bin/env bash
set -Eeuo pipefail
file="${1:-filters.txt}"
[[ -s "$file" ]] || { echo "ERROR: missing/empty $file" >&2; exit 1; }
awk '
BEGIN{bad=0}
{
  sub(/\r$/,"")
  if($0==""||$0~/^!/)next
  if($0~/[[:space:]]/){print "whitespace: " NR;bad=1}
  if($0~/\$/){print "modifier: " NR;bad=1}
  if($0~/##|#@#|#\?#|#\$#|#%#|#\^#|#@%\?#/){print "cosmetic: " NR;bad=1}
  if($0~/\+js\(|:has-text\(|:contains\(|:matches-css\(|:xpath\(|:style\(/){print "scriptlet/procedural: " NR;bad=1}
  if($0~/^\/.*\/$/){print "regex: " NR;bad=1}
  if($0~/^@@\|\|/){x=substr($0,5)} else if($0~/^\|\|/){x=substr($0,3)} else if($0~/^\|https?:\/\//){next} else {print "unsupported prefix: " NR;bad=1;next}
  host=x;sub(/[\/?#\^|].*$/,"",host)
  if(host!~/^[A-Za-z0-9.-]+$/||host!~/\./||host~/^\.|\.$|\.\./){print "bad host: " NR;bad=1}
}
END{exit bad}
' "$file"
echo "OK: $file passes legacy-compatible validator"
