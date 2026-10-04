// PM2 config. deploy.sh starts/reloads it; by hand: pm2 reload ecosystem.config.cjs --only file-server-v2
// Serves /library and /cloud on 8090. Runs on the default Node (nvm default 24 on the droplet).
const common = {
  script: 'dist/server/entry.mjs',
  cwd: __dirname,
  max_memory_restart: '250M',
  env: {
    NODE_ENV: 'production', HOST: '127.0.0.1', NODE_OPTIONS: '--max-old-space-size=200',
    LOG_REQUESTS: '0', // nginx's access log already records every request
    DROPLET_BASE: 'https://tipitaka.lk/fs-dl', DROPLET_SHARE: '40', // README: serving part of the downloads from the droplet
  },
}
module.exports = {
  apps: [
    { ...common, name: 'file-server-v2', env: { ...common.env, PORT: 8090 } },
  ],
}
