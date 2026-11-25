import { createAppServer } from './appServer.mjs';

const port = Number(process.env.APP_API_PORT || 3001);
const wsUrl = process.env.WS_SERVER_URL || 'ws://127.0.0.1:8080/ws';

const app = createAppServer({ wsUrl });

app.server.listen(port, () => {
  console.log(`[app-server] REST API listening on http://localhost:${port}`);
  console.log(`[app-server] Bridging to real-time WS server at ${wsUrl}`);
});

const shutdown = async () => {
  await app.close();
  process.exit(0);
};
process.on('SIGINT', shutdown);
process.on('SIGTERM', shutdown);
