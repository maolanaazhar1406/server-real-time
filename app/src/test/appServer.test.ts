// @vitest-environment node
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { createAppServer, AppServerInstance } from '../../server/appServer.mjs';

describe('app/server REST API & Sync', () => {
  let appSrv: AppServerInstance;
  let baseUrl = '';

  beforeAll(async () => {
    appSrv = createAppServer({ enableBridge: false });
    await new Promise<void>((resolve) => {
      appSrv.server.listen(0, '127.0.0.1', () => {
        const addr = appSrv.server.address() as { port: number };
        baseUrl = `http://127.0.0.1:${addr.port}`;
        resolve();
      });
    });
  });

  afterAll(async () => {
    await appSrv.close();
  });

  it('supports login, expired token 401, refresh, and logout', async () => {
    const loginRes = await fetch(`${baseUrl}/api/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        identifier: 'alice@example.com',
        password: 'password123',
        device_name: 'Vitest',
        access_ttl_seconds: 1,
      }),
    });
    expect(loginRes.status).toBe(200);
    const loginData = (await loginRes.json()) as { access_token: string; refresh_token: string };

    // Valid initially
    const me1 = await fetch(`${baseUrl}/api/auth/me`, {
      headers: { Authorization: `Bearer ${loginData.access_token}` },
    });
    expect(me1.status).toBe(200);

    // Wait 1.1s for expiry -> 401
    await new Promise((r) => setTimeout(r, 1100));
    const meExpired = await fetch(`${baseUrl}/api/auth/me`, {
      headers: { Authorization: `Bearer ${loginData.access_token}` },
    });
    expect(meExpired.status).toBe(401);

    // Refresh issues fresh token
    const refRes = await fetch(`${baseUrl}/api/auth/refresh`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refresh_token: loginData.refresh_token }),
    });
    expect(refRes.status).toBe(200);
    const refData = (await refRes.json()) as { access_token: string };

    const me2 = await fetch(`${baseUrl}/api/auth/me`, {
      headers: { Authorization: `Bearer ${refData.access_token}` },
    });
    expect(me2.status).toBe(200);

    // Logout revokes session
    const outRes = await fetch(`${baseUrl}/api/auth/logout`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${refData.access_token}` },
    });
    expect(outRes.status).toBe(200);

    const meAfterLogout = await fetch(`${baseUrl}/api/auth/me`, {
      headers: { Authorization: `Bearer ${refData.access_token}` },
    });
    expect(meAfterLogout.status).toBe(401);
  });

  it('supports multipart media upload, deduplicated messages, and delta sync', async () => {
    const regRes = await fetch(`${baseUrl}/api/auth/register`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        email: 'charlie@example.com',
        username: 'charlie',
        password: 'password123',
      }),
    });
    expect(regRes.status).toBe(201);
    const regData = (await regRes.json()) as { access_token: string };

    const form = new FormData();
    form.append('file', new Blob(['hello-file-data'], { type: 'text/plain' }), 'hello.txt');
    const upRes = await fetch(`${baseUrl}/api/media/upload`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${regData.access_token}` },
      body: form,
    });
    expect(upRes.status).toBe(201);
    const att = (await upRes.json()) as { url: string; filename: string };
    expect(att.filename).toBe('hello.txt');

    const dlRes = await fetch(`${baseUrl}${att.url}`);
    expect(dlRes.status).toBe(200);
    expect(await dlRes.text()).toBe('hello-file-data');

    const r1 = appSrv.store.recordMessage({
      topic: 'general',
      senderId: 'charlie',
      messageId: 'm-1',
      clientMsgId: 'cm-1',
      text: 'Sync test @alice',
    });
    const r2 = appSrv.store.recordMessage({
      topic: 'general',
      senderId: 'charlie',
      messageId: 'm-1',
      clientMsgId: 'cm-1',
      text: 'Sync test @alice',
    });
    expect(r1.created).toBe(true);
    expect(r2.created).toBe(false);

    const syncRes = await fetch(`${baseUrl}/api/sync?last_event_id=0`, {
      headers: { Authorization: `Bearer ${regData.access_token}` },
    });
    expect(syncRes.status).toBe(200);
    const syncBody = await syncRes.text();
    expect(syncBody).toContain('Sync test @alice');

    // Reply & Delete Message flow
    const replyRec = appSrv.store.recordMessage({
      topic: 'general',
      senderId: 'charlie',
      messageId: 'm-reply-1',
      text: 'Reply to m-1',
      replyTo: {
        message_id: 'm-1',
        sender_id: 'charlie',
        sender_name: 'charlie',
        text: 'Sync test @alice',
      },
    });
    expect(replyRec.created).toBe(true);

    // Other user (alice) cannot delete charlie's message -> 403
    const aliceLogin = await fetch(`${baseUrl}/api/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ identifier: 'alice@example.com', password: 'password123' }),
    });
    const aliceData = (await aliceLogin.json()) as { access_token: string };
    const forbiddenDel = await fetch(`${baseUrl}/api/messages/m-reply-1`, {
      method: 'DELETE',
      headers: { Authorization: `Bearer ${aliceData.access_token}` },
    });
    expect(forbiddenDel.status).toBe(403);

    // Owner (charlie) deletes message -> 200 and recorded in delta sync
    const okDel = await fetch(`${baseUrl}/api/messages/m-reply-1`, {
      method: 'DELETE',
      headers: { Authorization: `Bearer ${regData.access_token}` },
    });
    expect(okDel.status).toBe(200);

    const syncAfterDel = await fetch(`${baseUrl}/api/sync?last_event_id=0`, {
      headers: { Authorization: `Bearer ${regData.access_token}` },
    });
    const delSyncText = await syncAfterDel.text();
    expect(delSyncText).toContain('delete_message');
  });
});
