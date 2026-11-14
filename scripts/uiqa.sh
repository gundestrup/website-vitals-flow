#!/usr/bin/env bash
# static-asset hygiene: alpine-csp parse hazards, external resources,
# balanced CSS braces — the classes node --check and go vet cannot see.
set -u; fail=0
d=internal/server/static
[[ -d "$d" ]] || exit 0

# alpine-csp cannot parse: x-html, literal escaped quotes, ||{} guards
# (pages live in .tmpl now — same Alpine-expression rules apply there)
if grep -rnE "x-html=|\\\\'|\|\|[[:space:]]*\{\}" "$d" --include='*.html' --include='*.tmpl'; then
  echo "uiqa FAIL: alpine-csp hazard in html"; fail=1
fi

# all assets must be vendored — no external references
if grep -rnE '(src|href)="https?://' "$d" --include='*.html' --include='*.tmpl'; then
  echo "uiqa FAIL: external asset reference in html"; fail=1
fi
if grep -rnE 'url\(https?://' "$d" --include='*.css'; then
  echo "uiqa FAIL: external url() in css"; fail=1
fi

# CSS brace balance per file
for f in "$d"/*.css; do
  [[ -e "$f" ]] || continue
  o=$(tr -cd '{' <"$f" | wc -c | tr -d ' '); c=$(tr -cd '}' <"$f" | wc -c | tr -d ' ')
  if [[ "$o" != "$c" ]]; then echo "uiqa FAIL: $f unbalanced braces $o/$c"; fail=1; fi
done

exit $fail
