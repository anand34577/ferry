#!/usr/bin/env sh
# Publishes docs/ to the GitHub wiki. docs/ stays the source of truth: edit there, then run this.
# Usage: scripts/wiki-sync.sh [wiki-checkout-dir]   (default: ../ferry.wiki, cloned if missing)
# DRY_RUN=1 writes the pages without committing or pushing.
set -eu
cd "$(dirname "$0")/.."
REPO_URL="https://github.com/anand34577/ferry"
WIKI="${1:-../ferry.wiki}"
[ -d "$WIKI/.git" ] || git clone "$REPO_URL.wiki.git" "$WIKI"
git -C "$WIKI" pull -q --ff-only || true

# docs file → wiki page name
page() {
  case "$1" in
    README) echo Home ;;
    getting-started) echo Getting-Started ;;
    install-docker) echo Install-with-Docker ;;
    install-binary) echo Install-the-Program ;;
    https) echo HTTPS ;;
    user-guide) echo User-Guide ;;
    android) echo Android-App ;;
    configuration) echo Configuration ;;
    administration) echo Administration ;;
    sso) echo Single-Sign-On ;;
    troubleshooting) echo Troubleshooting ;;
    development) echo Development ;;
    *) echo "$1" ;;
  esac
}

# Rewrite "(name.md)" / "(name.md#x)" links to wiki pages and "../path" links to the repository.
for f in docs/*.md; do
  n=$(basename "$f" .md)
  out="$WIKI/$(page "$n").md"
  script=""
  for g in docs/*.md; do
    m=$(basename "$g" .md)
    script="$script
s#](${m}\\.md#]($(page "$m")#g"
  done
  script="$script
s#](\\.\\./\\([^)]*\\))#]($REPO_URL/blob/main/\\1)#g"
  sed "$script" "$f" > "$out"
  echo "wrote $out"
done

cat > "$WIKI/_Sidebar.md" <<'EOF'
**[Home](Home)**

**Start here**
- [Getting started](Getting-Started)
- [Install with Docker](Install-with-Docker)
- [Install the program](Install-the-Program)
- [HTTPS](HTTPS)

**Using Ferry**
- [User guide](User-Guide)
- [Android app](Android-App)

**Running a server**
- [Configuration](Configuration)
- [Administration](Administration)
- [Single sign-on (OIDC)](Single-Sign-On)
- [Troubleshooting](Troubleshooting)

**Contributing**
- [Development](Development)
EOF
cat > "$WIKI/_Footer.md" <<EOF
Generated from [\`docs/\`]($REPO_URL/tree/main/docs) — to fix something, edit the file there. · [Report a problem]($REPO_URL/issues)
EOF

[ "${DRY_RUN:-}" = 1 ] && { echo "dry run: not pushed"; exit 0; }
git -C "$WIKI" add -A
if git -C "$WIKI" diff --cached --quiet; then
  echo "wiki already up to date"
else
  git -C "$WIKI" commit -q -m "Sync documentation from docs/"
  git -C "$WIKI" push -q
  echo "wiki updated: $REPO_URL/wiki"
fi
