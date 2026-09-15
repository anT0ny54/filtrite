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
  if($0~/##|#@#|#\?#|#\$#|#%#|#\^#|#@%\?#/){print "cosmetic: " NR;bad=1}
  if($0~/\+js\(|:has-text\(|:contains\(|:matches-css\(|:xpath\(|:style\(/){print "scriptlet/procedural: " NR;bad=1}
  if($0~/^\/.*\/$/){print "regex: " NR;bad=1}

  # Secondary sanity check for the modifier subset emitted by filter.go.
  # Chromium ruleset_converter remains the authoritative final parser.
  line=$0
  dollar=0
  for(i=length(line);i>=1;i--){ if(substr(line,i,1)=="$"){dollar=i;break} }
  if(dollar>0){
    mods=substr(line,dollar+1)
    line=substr(line,1,dollar-1)
    if(mods==""){print "empty modifier: " NR;bad=1}
    else{
      n=split(mods,parts,",")
      for(j=1;j<=n;j++){
        p=parts[j]
        if(p=="third-party"||p=="~third-party"||p=="match-case")continue
        if(p~/^domain=[^,]+$/)continue
        print "unsupported modifier: " NR;bad=1
      }
    }
  }

  if(line~/^@@\|\|/){x=substr(line,5)} else if(line~/^\|\|/){x=substr(line,3)} else if(line~/^@@\|https?:\/\// || line~/^\|https?:\/\//){next} else {print "unsupported prefix: " NR;bad=1;next}
  host=x;sub(/[\/?#\^|].*$/,"",host)
  if(host!~/^[A-Za-z0-9.-]+$/||host!~/\./||host~/^\.|\.$|\.\./){print "bad host: " NR;bad=1}
}
END{exit bad}
' "$file"
echo "OK: $file passes legacy-compatible validator"
