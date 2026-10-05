#!/usr/bin/env bash
# Build and deploy to the droplet:   ./deploy.sh
# Same mechanism as the tipitaka.lk Go servers: a linux binary in ~/www/file_server (= /var/www/html/file_server),
# run by the systemd unit file_server (server/file_server.service), restarted with sudo over ssh.
# Never overwrites the server's config/secrets.json or data/*.db. Keeps the previous binary as file_server.prev.
# Override the target with DEPLOY_HOST.
set -euo pipefail
cd "$(dirname "$0")"

host=${DEPLOY_HOST:-tipitaka.lk}
dir=www/file_server
port=8090

echo "==> test + build"
node scripts/build-assets.mjs >/dev/null
(cd server && go test ./... >/dev/null && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/file_server_linux_intel .)

echo "==> upload to $host:~/$dir"
ssh "$host" "mkdir -p ~/$dir/config ~/$dir/data"
rsync -az server/bin/file_server_linux_intel "$host:$dir/file_server.new"
rsync -az --delete server/dist/ "$host:$dir/dist/"
rsync -az --delete deploy/nginx/ "$host:$dir/nginx/"
rsync -az config/library.json config/cloud.json config/secrets.example.json "$host:$dir/config/"
rsync -az server/file_server.service "$host:$dir/"
ssh "$host" "test -f ~/$dir/config/secrets.json" || { echo "missing ~/$dir/config/secrets.json on the server (see secrets.example.json)" >&2; exit 1; }
ssh "$host" "cd ~/$dir && { test -f file_server && cp file_server file_server.prev || true; } && mv file_server.new file_server && chmod +x file_server"

echo "==> restart (sudo)"
ssh -t "$host" "sudo systemctl restart file_server"

echo "==> health"
for i in $(seq 1 20); do
  if out=$(ssh "$host" "curl -fsS http://127.0.0.1:$port/library/api/health" 2>/dev/null); then echo "$out"; exit 0; fi
  sleep 1
done
echo "health check failed - see: ssh $host 'journalctl -u file_server -n 50'" >&2
echo "rollback: ssh -t $host 'cd ~/$dir && mv file_server.prev file_server && sudo systemctl restart file_server'" >&2
exit 1
