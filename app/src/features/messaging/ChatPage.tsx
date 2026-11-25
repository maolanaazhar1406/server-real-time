import React, { useEffect, useMemo, useRef, useState } from 'react';
import { Check, CheckCheck, Clock, FileText, Paperclip, Send, Smile } from 'lucide-react';
import { APP_CONFIG } from '@/core/config/env';
import { syncEngine } from '@/core/sync/syncEngine';
import { Attachment, ChatMessage, TypingState } from '@/core/types';
import { PresencePanel } from '@/features/presence/PresencePanel';
import { useNetworkStatus, useStoreSelector } from '@/shared/hooks';
import { Avatar, Badge, Button, Skeleton, useToast } from '@/shared/ui';
import { formatFileSize, formatTime } from '@/shared/utils/date';
import { messagingService } from './messagingService';

const QUICK_EMOJIS = ['👍', '🚀', '✅', '❤️'];
const EMPTY_MESSAGES: ChatMessage[] = [];
const EMPTY_TYPING: TypingState[] = [];

function renderStatusIcon(status: ChatMessage['status']) {
  switch (status) {
    case 'pending':
    case 'sending':
      return <Clock size={12} aria-label="pending" />;
    case 'sent':
      return <Check size={12} aria-label="sent" />;
    case 'delivered':
      return <CheckCheck size={12} aria-label="delivered" />;
    case 'read':
      return <CheckCheck size={12} style={{ color: 'var(--accent)' }} aria-label="read" />;
    case 'failed':
      return <span style={{ color: 'var(--danger)' }}>!</span>;
  }
}

export const ChatPage: React.FC<{ topic: string }> = ({ topic }) => {
  const { pushToast } = useToast();
  const { isConnected } = useNetworkStatus();
  const user = useStoreSelector((s) => s.session?.user);
  const rooms = useStoreSelector((s) => s.rooms);
  const messages = useStoreSelector((s) => s.messagesByTopic[topic] ?? EMPTY_MESSAGES);
  const rawTypingList = useStoreSelector((s) => s.typingByTopic[topic] ?? EMPTY_TYPING);
  const typingList = useMemo(
    () => rawTypingList.filter((t) => t.expires_at > Date.now()),
    [rawTypingList],
  );
  const presenceMap = useStoreSelector((s) => s.presenceByUserId);

  const [text, setText] = useState('');
  const [uploading, setUploading] = useState(false);
  const [pendingAttachment, setPendingAttachment] = useState<Attachment | null>(null);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [hasMore, setHasMore] = useState(true);
  const [emojiMenuFor, setEmojiMenuFor] = useState<string | null>(null);

  const fileInputRef = useRef<HTMLInputElement>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const typingTimeoutRef = useRef<number | null>(null);

  const activeRoom = rooms.find((r) => r.topic === topic);

  useEffect(() => {
    messagingService.selectRoom(topic);
    setHasMore(true);
  }, [topic]);

  useEffect(() => {
    if (typeof bottomRef.current?.scrollIntoView === 'function') {
      bottomRef.current.scrollIntoView({ behavior: 'smooth' });
    }
  }, [messages.length, topic]);

  const handleLoadOlder = async () => {
    const oldest = messages[0]?.event_id;
    if (!oldest) {
      setHasMore(false);
      return;
    }
    setLoadingOlder(true);
    try {
      const res = await syncEngine.loadRoomHistory(topic, oldest);
      setHasMore(res.hasMore);
    } catch {
      pushToast({ title: 'Unable to load older history offline', variant: 'warning' });
    } finally {
      setLoadingOlder(false);
    }
  };

  const handleTypingChange = (val: string) => {
    setText(val);
    if (!isConnected) return;
    messagingService.sendTyping(topic, val.length > 0);
    if (typingTimeoutRef.current !== null) {
      window.clearTimeout(typingTimeoutRef.current);
    }
    typingTimeoutRef.current = window.setTimeout(() => {
      messagingService.sendTyping(topic, false);
    }, 2000);
  };

  const handleFileChange = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    setUploading(true);
    try {
      const att = await messagingService.uploadMedia(file);
      setPendingAttachment(att);
      pushToast({ title: 'Attachment ready', description: att.filename, variant: 'success' });
    } catch (err) {
      pushToast({
        title: 'Upload failed',
        description: err instanceof Error ? err.message : undefined,
        variant: 'error',
      });
    } finally {
      setUploading(false);
      if (fileInputRef.current) fileInputRef.current.value = '';
    }
  };

  const handleSend = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!text.trim() && !pendingAttachment) return;
    const msgText = text;
    const att = pendingAttachment ?? undefined;
    setText('');
    setPendingAttachment(null);
    messagingService.sendTyping(topic, false);
    await messagingService.sendMessage(topic, msgText, att);
  };

  return (
    <div className="chat-workspace">
      <div className="chat-main">
        <div className="chat-header">
          <div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <strong style={{ fontSize: 16 }}>#{activeRoom?.name || topic}</strong>
              <Badge variant="primary">{messages.length} msgs</Badge>
            </div>
            <div style={{ fontSize: 12, color: 'var(--text-secondary)' }}>
              {activeRoom?.description || 'Real-time topic stream'}
            </div>
          </div>
          {!isConnected && (
            <Badge variant="warning">Offline Queue Active — messages will sync automatically</Badge>
          )}
        </div>

        <div className="chat-messages">
          {hasMore && messages.length >= 20 && (
            <div style={{ textAlign: 'center' }}>
              <Button variant="ghost" size="sm" loading={loadingOlder} onClick={() => void handleLoadOlder()}>
                Load earlier messages
              </Button>
            </div>
          )}

          {!rooms.length && messages.length === 0 ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
              <Skeleton height="52px" />
              <Skeleton height="52px" />
            </div>
          ) : messages.length === 0 ? (
            <div style={{ color: 'var(--text-muted)', textAlign: 'center', marginTop: 40 }}>
              No messages in #{topic} yet. Say hello!
            </div>
          ) : (
            messages.map((m) => {
              const isOwn = m.sender_id === user?.id;
              const senderPresence = presenceMap[m.sender_id]?.status;
              const mediaUrl = m.attachment
                ? `${APP_CONFIG.apiBaseUrl}${m.attachment.url}`
                : undefined;
              const isImage = m.attachment?.mime_type?.startsWith('image/');

              return (
                <div
                  key={m.message_id}
                  className={`msg-row ${isOwn ? 'msg-row--own' : ''}`}
                >
                  <Avatar
                    name={m.sender_name || m.sender_id}
                    size="sm"
                    status={senderPresence}
                  />
                  <div className="msg-bubble">
                    <div className="msg-meta">
                      <strong style={{ color: 'var(--text-primary)' }}>
                        {m.sender_name || m.sender_id}
                      </strong>
                      <span>{formatTime(m.timestamp)}</span>
                    </div>
                    {m.text && <div className="msg-text">{m.text}</div>}
                    {m.attachment && (
                      <div className="msg-attachment">
                        {isImage && mediaUrl ? (
                          <a href={mediaUrl} target="_blank" rel="noreferrer">
                            <img src={mediaUrl} alt={m.attachment.filename} />
                          </a>
                        ) : (
                          <>
                            <FileText size={16} />
                            <a href={mediaUrl} target="_blank" rel="noreferrer">
                              {m.attachment.filename} ({formatFileSize(m.attachment.size)})
                            </a>
                          </>
                        )}
                      </div>
                    )}

                    <div className="msg-footer">
                      <div className="msg-reactions">
                        {Object.entries(m.reactions ?? {}).map(([emoji, reactors]) => (
                          <button
                            key={emoji}
                            type="button"
                            className="reaction-chip"
                            onClick={() => messagingService.toggleReaction(topic, m.message_id, emoji)}
                          >
                            {emoji} {reactors.length}
                          </button>
                        ))}
                        <div style={{ position: 'relative' }}>
                          <button
                            type="button"
                            className="icon-btn"
                            style={{ padding: 2 }}
                            onClick={() =>
                              setEmojiMenuFor((prev) => (prev === m.message_id ? null : m.message_id))
                            }
                            aria-label="Add reaction"
                          >
                            <Smile size={13} />
                          </button>
                          {emojiMenuFor === m.message_id && (
                            <div
                              style={{
                                position: 'absolute',
                                bottom: '100%',
                                left: 0,
                                display: 'flex',
                                gap: 4,
                                background: 'var(--bg-surface)',
                                border: '1px solid var(--border-color)',
                                borderRadius: 999,
                                padding: '3px 6px',
                                zIndex: 20,
                              }}
                            >
                              {QUICK_EMOJIS.map((em) => (
                                <button
                                  key={em}
                                  type="button"
                                  className="icon-btn"
                                  style={{ padding: 2 }}
                                  onClick={() => {
                                    messagingService.toggleReaction(topic, m.message_id, em);
                                    setEmojiMenuFor(null);
                                  }}
                                >
                                  {em}
                                </button>
                              ))}
                            </div>
                          )}
                        </div>
                      </div>

                      {isOwn && (
                        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
                          <span>{m.status}</span>
                          {renderStatusIcon(m.status)}
                        </span>
                      )}
                    </div>
                  </div>
                </div>
              );
            })
          )}
          <div ref={bottomRef} />
        </div>

        <div className="typing-bar">
          {typingList.length > 0 && (
            <span>
              {typingList.map((t) => t.display_name || t.user_id).join(', ')} is typing...
            </span>
          )}
        </div>

        <form className="composer" onSubmit={(e) => void handleSend(e)}>
          {pendingAttachment && (
            <div className="msg-attachment" style={{ marginTop: 0, justifyContent: 'space-between' }}>
              <span>
                Attached: <strong>{pendingAttachment.filename}</strong> ({formatFileSize(pendingAttachment.size)})
              </span>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => setPendingAttachment(null)}
              >
                Remove
              </Button>
            </div>
          )}
          <div className="composer__row">
            <input
              ref={fileInputRef}
              type="file"
              style={{ display: 'none' }}
              onChange={(e) => void handleFileChange(e)}
            />
            <Button
              type="button"
              variant="secondary"
              size="md"
              loading={uploading}
              onClick={() => fileInputRef.current?.click()}
              aria-label="Upload attachment"
            >
              <Paperclip size={16} />
            </Button>
            <input
              className="composer__input"
              value={text}
              onChange={(e) => handleTypingChange(e.target.value)}
              placeholder={`Message #${topic} (supports @mentions)`}
            />
            <Button type="submit" size="md">
              <Send size={16} /> Send
            </Button>
          </div>
        </form>
      </div>

      <PresencePanel />
    </div>
  );
};
