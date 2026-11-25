import { spawn } from 'node:child_process';

const api = spawn(process.execPath, ['server/index.mjs'], { stdio: 'inherit' });
const vite = spawn(process.execPath, ['node_modules/vite/bin/vite.js'], { stdio: 'inherit' });

const cleanup = () => {
  api.kill('SIGTERM');
  vite.kill('SIGTERM');
};

process.on('SIGINT', cleanup);
process.on('SIGTERM', cleanup);

api.on('exit', (code) => {
  vite.kill('SIGTERM');
  process.exit(code ?? 0);
});
vite.on('exit', (code) => {
  api.kill('SIGTERM');
  process.exit(code ?? 0);
});
