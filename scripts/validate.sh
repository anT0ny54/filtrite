#!/usr/bin/env bash
set -Eeuo pipefail
file="${1:-filters/adblock.txt}"
[[ -s "$file" ]] || { echo "ERROR: missing/empty $file" >&2; exit 1; }
awk '
function valid_domain(host,    n, labels, i, label, all_numeric) {
  if(length(host)==0 || length(host)>253)return 0
  n=split(host,labels,".")
  if(n<2)return 0
  all_numeric=1
  for(i=1;i<=n;i++){
    label=labels[i]
    if(length(label)==0 || length(label)>63 || label~/^-/ || label~/-$/ || label!~/^[A-Za-z0-9-]+$/)return 0
    if(label~/[A-Za-z-]/)all_numeric=0
  }
  return !all_numeric
}
BEGIN{bad=0}
{
  sub(/\r$/,"")
  if($0==""||$0~/^!/)next
  if($0~/[[:space:]]/){print "whitespace: " NR;bad=1}

  # Cosmetic and scriptlet markers are matched as literal strings (index),
  # not regexes, so no ERE metacharacter escaping is involved and behavior
  # cannot differ between awk implementations. Mirrors the marker list in
  # internal/filter/filter.go.
  if(index($0,"##")||index($0,"#@#")||index($0,"#?#")||index($0,"#$#")||index($0,"#%#")||index($0,"#^#")||index($0,"#@%?#")){print "cosmetic: " NR;bad=1}
  if(index($0,"+js(")||index($0,":has-text(")||index($0,":contains(")||index($0,":matches-css(")||index($0,":xpath(")||index($0,":style(")){print "scriptlet/procedural: " NR;bad=1}
  if($0~/^\/.*\/$/){print "regex: " NR;bad=1}

  # Secondary sanity check for the modifier subset emitted by filter.go.
  line=$0
  exception=0
  if(line~/^@@/) exception=1
  dollar=0
  if(match(line,/\$[^$]*$/)) dollar=RSTART
  if(dollar>0){
    mods=substr(line,dollar+1)
    line=substr(line,1,dollar-1)
    if(mods==""){print "empty modifier: " NR;bad=1}
    else{
      n=split(mods,parts,",")
      delete seen
      ntypes=0;nact=0;neg=0;pos=0
      for(j=1;j<=n;j++){
        p=parts[j]
        name=p;sub(/^~/,"",name);sub(/=.*$/,"",name)
        if(name in seen){print "duplicate/conflicting modifier: " NR;bad=1}
        seen[name]=1
        if(p=="third-party"||p=="~third-party"||p=="match-case")continue
        if(p~/^domain=[^,]+$/){
          dl=substr(p,8)
          nd=split(dl,doms,"|")
          delete dseen;delete dbase
          for(k=1;k<=nd;k++){
            d=doms[k];ex=0
            if(d~/^~/){ex=1;d=substr(d,2)}
            if(d!~/^[a-z0-9.-]+$/||!valid_domain(d)){print "bad domain= entry: " NR;bad=1;continue}
            if((ex d) in dseen){print "duplicate domain= entry: " NR;bad=1}
            if((d in dbase)&&dbase[d]!=ex){print "conflicting domain= entry: " NR;bad=1}
            dseen[ex d]=1;dbase[d]=ex
          }
          continue
        }
        if(p~/^~?(script|image|stylesheet|object|xmlhttprequest|object-subrequest|subdocument|ping|media|font|websocket|other)$/){
          ntypes++
          if(p~/^~/)neg++;else pos++
          continue
        }
        if(p=="document"||p=="genericblock"){
          nact++
          if(!exception){print "activation-not-exception: " NR;bad=1}
          continue
        }
        print "unsupported modifier: " NR;bad=1
      }
      if(neg>0&&pos>0){print "mixed-sign resource types: " NR;bad=1}
      if(ntypes>0&&nact>0){print "activation combined with resource types: " NR;bad=1}
    }
  }

  # Fully anchored URLs need their own checks before the early exit: validate
  # scheme, URL character set, percent escapes, authority, and hostname. The
  # URL character class is the single source of truth shared with
  # anchoredURLChars in internal/filter/filter.go; both sides must stay
  # identical so the Go stage can never emit a rule this validator rejects.
  if(line~/^@@\|https?:\/\// || line~/^\|https?:\/\//){
    url=line
    if(url~/^@@\|/) url=substr(url,4)
    else url=substr(url,2)

    # filter.go emits "|scheme://host/path|" for end-anchored rules. Exactly one
    # trailing "|" is the end anchor and is not part of the URL; any other "|"
    # (including a second trailing one) stays in the URL and fails the
    # character-set check below, matching validAnchoredTail in filter.go.
    if(url~/\|$/) url=substr(url,1,length(url)-1)

    if(url!~/^https?:\/\/[A-Za-z0-9.-]+([\/?#][A-Za-z0-9._~!$&()*+,;=:@%\/?#-]*)?$/){
      print "malformed anchored URL: " NR;bad=1;next
    }
    if(url~/%([^0-9A-Fa-f]|$)|%[0-9A-Fa-f]([^0-9A-Fa-f]|$)/){
      print "bad percent escape in anchored URL: " NR;bad=1;next
    }

    authority=url
    sub(/^https?:\/\//,"",authority)
    host=authority
    sub(/[\/?#].*$/,"",host)
    if(host!~/^[A-Za-z0-9.-]+$/||host!~/\./||host~/^\.|\.$|\.\./){
      print "bad host: " NR;bad=1;next
    }
    if(host~/^[0-9.]+$/){print "ip literal host: " NR;bad=1;next}
    if(host~/[A-Z]/){print "non-canonical (upper-case) host: " NR;bad=1;next}
    if(!valid_domain(host)){print "bad hostname label or length: " NR;bad=1;next}
    next
  }

  if(line~/^@@\|\|/){x=substr(line,5)} else if(line~/^\|\|/){x=substr(line,3)} else {print "unsupported prefix: " NR;bad=1;next}
  # In domain-anchored network rules, `|` can only be one final end anchor.
  # Strip that one for host parsing and reject internal/repeated pipes.
  if(index(x,"|")){
    if(x !~ /\|$/){print "malformed end anchor: " NR;bad=1;next}
    x=substr(x,1,length(x)-1)
    if(index(x,"|")){print "malformed end anchor: " NR;bad=1;next}
  }
  host=x;sub(/[\/?#\^|].*$/,"",host)

  # Bare-domain block without the $third-party guard the Go parser always
  # emits. The hostname shape mirrors validDomain in internal/filter: at
  # least two lower-case labels with proper label boundaries (generated
  # output is already canonicalized), excluding all-numeric/IP-literal hosts.
  if(!exception && dollar==0 \
     && line ~ /^\|\|[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+\^$/ \
     && line !~ /^\|\|[0-9.]+\^$/){print "missing third-party guard: " NR;bad=1}

  if(host!~/^[A-Za-z0-9.-]+$/||host!~/\./||host~/^\.|\.$|\.\./){print "bad host: " NR;bad=1}
  else if(host~/^[0-9.]+$/){print "ip literal host: " NR;bad=1}
  else if(host~/[A-Z]/){print "non-canonical (upper-case) host: " NR;bad=1}
  else if(!valid_domain(host)){print "bad hostname label or length: " NR;bad=1}
}
END{exit bad}
' "$file"
echo "OK: $file passes legacy-compatible validator"
