#!/usr/bin/env bash
# Build locally and deploy to the droplet:   ./deploy.sh   (PM2 app file-server-v2: /library, /cloud on 8090)
# Safe to re-run: it never overwrites the server's data/*.db (live download counts) or config/secrets.json;
# those are only copied if missing on the server. Override the target with DEPLOY_HOST / DEPLOY_DIR.
set -euo pipefail
cd "$(dirname "$0")"

app=file-server-v2; port=8090; prefix=library
host=${DEPLOY_HOST:-tipitaka.lk}
dir=${DEPLOY_DIR:-file-server-v2}
remote() { ssh "$host" "source ~/.nvm/nvm.sh >/dev/null && cd ~/$dir && $1"; }

echo "==> build"
npm run build

echo "==> upload to $host:~/$dir"
ssh "$host" "mkdir -p ~/$dir/src ~/$dir/config ~/$dir/data"
rsync -az --delete dist/ "$host:$dir/dist/"
rsync -az --delete src/lib/ "$host:$dir/src/lib/"
rsync -az --delete scripts/ "$host:$dir/scripts/"
rsync -az --delete deploy/ "$host:$dir/deploy/"
rsync -az package.json package-lock.json ecosystem.config.cjs "$host:$dir/"
rsync -az config/library.json config/cloud.json config/secrets.example.json "$host:$dir/config/"
# copied only when missing on the server
rsync -az --ignore-existing config/secrets.json "$host:$dir/config/"
for site in library cloud; do
  if ! ssh "$host" "test -f ~/$dir/data/$site.db"; then
    echo "    first deploy: copying data/$site.db"
    node -e "new (require('node:sqlite').DatabaseSync)('data/$site.db').exec('PRAGMA wal_checkpoint(TRUNCATE)')"
    rsync -az "data/$site.db" "$host:$dir/data/"
  fi
done

echo "==> dependencies"
remote 'sum=$(sha1sum package-lock.json | cut -d" " -f1); if [ "$(cat .deps-sum 2>/dev/null)" != "$sum" ]; then npm ci --omit=dev --no-audit --no-fund && echo "$sum" > .deps-sum; else echo "    unchanged"; fi'

echo "==> (re)start $app"
remote "pm2 startOrReload ecosystem.config.cjs --only $app --update-env && pm2 save >/dev/null"

echo "==> health"
sleep 2
remote "curl -fsS http://127.0.0.1:$port/$prefix/api/health" && echo || { echo "health check failed - see: pm2 logs $app" >&2; exit 1; }
