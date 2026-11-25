import type { WireEnvelope } from '@/core/types';

function decodeBase64UTF8(b64: string): string {
  const binary = atob(b64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) {
    bytes[i] = binary.charCodeAt(i);
  }
  return new TextDecoder().decode(bytes);
}

export interface DecodedWorkerFrame {
  id: number;
  envelope: WireEnvelope | null;
  parsedPayload: unknown;
  error?: string;
}

export function decodeRawEnvelope(raw: string): {
  envelope: WireEnvelope | null;
  parsedPayload: unknown;
  error?: string;
} {
  try {
    const env = JSON.parse(raw) as WireEnvelope;
    let parsedPayload: unknown = null;
    if (env.payload) {
      const decodedText = decodeBase64UTF8(env.payload);
      if (decodedText.startsWith('{') || decodedText.startsWith('[')) {
        try {
          parsedPayload = JSON.parse(decodedText);
        } catch {
          parsedPayload = decodedText;
        }
      } else {
        parsedPayload = decodedText;
      }
    }
    return { envelope: env, parsedPayload };
  } catch (err) {
    return {
      envelope: null,
      parsedPayload: null,
      error: err instanceof Error ? err.message : 'Decode failed',
    };
  }
}

if (typeof self !== 'undefined' && typeof self.addEventListener === 'function' && typeof window === 'undefined') {
  self.addEventListener('message', (event: MessageEvent<{ id: number; raw: string }>) => {
    const { id, raw } = event.data;
    const result = decodeRawEnvelope(raw);
    self.postMessage({ id, ...result } satisfies DecodedWorkerFrame);
  });
}
