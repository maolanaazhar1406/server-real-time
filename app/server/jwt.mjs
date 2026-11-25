import crypto from 'node:crypto';

const DEFAULT_SECRET = process.env.JWT_SECRET || 'pulsert-dev-secret-change-in-prod';

function base64UrlEncode(input) {
  const buf = Buffer.isBuffer(input) ? input : Buffer.from(input, 'utf8');
  return buf.toString('base64').replace(/=/g, '').replace(/\+/g, '-').replace(/\//g, '_');
}

function base64UrlDecode(str) {
  const pad = str.length % 4 === 0 ? '' : '='.repeat(4 - (str.length % 4));
  const b64 = str.replace(/-/g, '+').replace(/_/g, '/') + pad;
  return Buffer.from(b64, 'base64');
}

export class JWTManager {
  constructor(secret = DEFAULT_SECRET, accessTTLSeconds = 900, refreshTTLSeconds = 7 * 24 * 3600) {
    this.secret = Buffer.from(secret, 'utf8');
    this.accessTTLSeconds = accessTTLSeconds;
    this.refreshTTLSeconds = refreshTTLSeconds;
  }

  sign(claims) {
    const header = base64UrlEncode(JSON.stringify({ alg: 'HS256', typ: 'JWT' }));
    const payload = base64UrlEncode(JSON.stringify(claims));
    const signingInput = `${header}.${payload}`;
    const sig = base64UrlEncode(
      crypto.createHmac('sha256', this.secret).update(signingInput).digest(),
    );
    return `${signingInput}.${sig}`;
  }

  generatePair(userId, username, deviceId, refreshJti, overrideAccessTTLSeconds) {
    const now = Math.floor(Date.now() / 1000);
    const accessTTL = overrideAccessTTLSeconds > 0 ? overrideAccessTTLSeconds : this.accessTTLSeconds;

    const access_token = this.sign({
      sub: userId,
      username,
      device_id: deviceId,
      token_type: 'access',
      iat: now,
      exp: now + accessTTL,
      jti: crypto.randomBytes(12).toString('hex'),
    });

    const refresh_token = this.sign({
      sub: userId,
      username,
      device_id: deviceId,
      token_type: 'refresh',
      iat: now,
      exp: now + this.refreshTTLSeconds,
      jti: refreshJti,
    });

    return { access_token, refresh_token, expires_in: accessTTL };
  }

  verify(token) {
    const parts = String(token || '').split('.');
    if (parts.length !== 3) {
      throw new Error('Invalid token');
    }
    const [header, payload, sig] = parts;
    const signingInput = `${header}.${payload}`;
    const expected = crypto.createHmac('sha256', this.secret).update(signingInput).digest();
    const got = base64UrlDecode(sig);
    if (got.length !== expected.length || !crypto.timingSafeEqual(got, expected)) {
      throw new Error('Invalid signature');
    }
    const claims = JSON.parse(base64UrlDecode(payload).toString('utf8'));
    const now = Math.floor(Date.now() / 1000);
    if (typeof claims.exp === 'number' && now >= claims.exp) {
      const err = new Error('Token expired');
      err.code = 'TOKEN_EXPIRED';
      throw err;
    }
    return claims;
  }
}

export function hashPassword(password) {
  const salt = crypto.randomBytes(16);
  const hash = crypto.createHash('sha256').update(Buffer.concat([salt, Buffer.from(password, 'utf8')])).digest();
  return `${salt.toString('hex')}:${hash.toString('hex')}`;
}

export function verifyPassword(password, stored) {
  const [saltHex, hashHex] = String(stored || '').split(':');
  if (!saltHex || !hashHex) return false;
  const salt = Buffer.from(saltHex, 'hex');
  const expected = Buffer.from(hashHex, 'hex');
  const actual = crypto.createHash('sha256').update(Buffer.concat([salt, Buffer.from(password, 'utf8')])).digest();
  return actual.length === expected.length && crypto.timingSafeEqual(actual, expected);
}
