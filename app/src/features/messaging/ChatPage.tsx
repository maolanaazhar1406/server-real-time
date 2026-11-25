import React, { useEffect, useMemo, useRef, useState } from 'react';
import {
  Activity,
  AtSign,
  Check,
  CheckCheck,
  Clock,
  FileText,
  Hash,
  Inbox,
  Layers,
  Paperclip,
  Plus,
  RefreshCw,
  Send,
  ShieldCheck,
  Smile,
  Users,
  Wifi,
} from 'lucide-react';
import { APP_CONFIG } from '@/core/config/env';
import { useRouter } from '@/core/router/router';
import { syncEngine } from '@/core/sync/syncEngine';
import { Attachment, ChatMessage, PresenceStatus, TypingState } from '@/core/types';
import { StatusSelector } from '@/features/presence/PresencePanel';
import { useNetworkStatus, useStoreSelector } from '@/shared/hooks';
import { Avatar, Badge, Button, Skeleton, StatusDot, useToast } from '@/shared/ui';
import { formatFileSize, formatRelativeTime, formatTime } from '@/shared/utils/date';
import { messagingService } from './messagingService';
import { NewRoomModal } from './NewRoomModal';

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

export const ChatPage: React.FC<{ topic: string; searchQuery?: string }> = ({
  topic,
  searchQuery = '',
}) => {
  const { navigate } = useRouter();
  const { pushToast } = useToast();
  const { isConnected, socketState } = useNetworkStatus();
  const user = useStoreSelector((s) => s.session?.user);
  const rooms = useStoreSelector((s) => s.rooms);
  const messagesByTopic = useStoreSelector((s) => s.messagesByTopic);
  const messages = useStoreSelector((s) => s.messagesByTopic[topic] ?? EMPTY_MESSAGES);
  const rawTypingList = useStoreSelector((s) => s.typingByTopic[topic] ?? EMPTY_TYPING);
  const typingList = useMemo(
    () => rawTypingList.filter((t) => t.expires_at > Date.now()),
    [rawTypingList],
  );
  const usersById = useStoreSelector((s) => s.usersById);
  const presenceMap = useStoreSelector((s) => s.presenceByUserId);
  const outbox = useStoreSelector((s) => s.outbox);
  const lastEventId = useStoreSelector((s) => s.lastEventId);
  const lastSyncAt = useStoreSelector((s) => s.lastSyncAt);

  const [text, setText] = useState('');
  const [uploading, setUploading] = useState(false);
  const [pendingAttachment, setPendingAttachment] = useState<Attachment | null>(null);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [hasMore, setHasMore] = useState(true);
  const [emojiMenuFor, setEmojiMenuFor] = useState<string | null>(null);
  const [composerEmojiOpen, setComposerEmojiOpen] = useState(false);
  const [newRoomOpen, setNewRoomOpen] = useState(false);
  const [channelTab, setChannelTab] = useState<'all' | 'active' | 'queued'>('all');

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

  const filteredRooms = useMemo(() => {
    const q = searchQuery.trim().toLowerCase();
    return rooms.filter((r) => {
      const roomMsgs = messagesByTopic[r.topic] ?? [];
      const roomQueued = outbox.filter((o) => o.topic === r.topic).length;
      if (channelTab === 'active' && roomMsgs.length === 0) return false;
      if (channelTab === 'queued' && roomQueued === 0) return false;
      if (!q) return true;
      return (
        r.name.toLowerCase().includes(q) ||
        r.topic.toLowerCase().includes(q) ||
        r.description.toLowerCase().includes(q) ||
        roomMsgs.some((m) => m.text.toLowerCase().includes(q))
      );
    });
  }, [rooms, messagesByTopic, outbox, channelTab, searchQuery]);

  const directoryUsers = useMemo(() => {
    const all = Object.values(usersById);
    return all.sort((a, b) => {
      const sa = presenceMap[a.id]?.status ?? 'OFFLINE';
      const sb = presenceMap[b.id]?.status ?? 'OFFLINE';
      const rank = (s: string) => (s === 'ONLINE' ? 0 : s === 'AWAY' ? 1 : 2);
      return rank(sa) - rank(sb) || a.display_name.localeCompare(b.display_name);
    });
  }, [usersById, presenceMap]);

  const sharedMedia = useMemo(
    () =>
      messages
        .filter((m) => Boolean(m.attachment))
        .map((m) => m.attachment!)
        .reverse(),
    [messages],
  );

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
    setComposerEmojiOpen(false);
    messagingService.sendTyping(topic, false);
    await messagingService.sendMessage(topic, msgText, att);
  };

  const activeUsersCount = directoryUsers.filter(
    (u) => (presenceMap[u.id]?.status ?? 'OFFLINE') !== 'OFFLINE',
  ).length;

  return (
    <div className="chat-workspace">
      {/* Column 2: Channels / Conversations List */}
      <aside className="conv-sidebar" aria-label="Channels">
        <div className="conv-sidebar__cta">
          <button
            type="button"
            className="cta-pill-btn"
            onClick={() => setNewRoomOpen(true)}
          >
            <Plus size={16} /> New Channel
          </button>
        </div>

        <div className="conv-tabs" role="tablist">
          <button
            type="button"
            role="tab"
            aria-selected={channelTab === 'all'}
            className={`conv-tab ${channelTab === 'all' ? 'conv-tab--active' : ''}`}
            onClick={() => setChannelTab('all')}
          >
            <Inbox size={14} /> All
            <span className="conv-tab__count">{rooms.length}</span>
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={channelTab === 'active'}
            className={`conv-tab ${channelTab === 'active' ? 'conv-tab--active' : ''}`}
            onClick={() => setChannelTab('active')}
          >
            <Layers size={14} /> Active
            <span className="conv-tab__count">
              {rooms.filter((r) => (messagesByTopic[r.topic]?.length ?? 0) > 0).length}
            </span>
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={channelTab === 'queued'}
            className={`conv-tab ${channelTab === 'queued' ? 'conv-tab--active' : ''}`}
            onClick={() => setChannelTab('queued')}
          >
            <Clock size={14} /> Queued
            <span className="conv-tab__count">{outbox.length}</span>
          </button>
        </div>

        <div className="conv-section-hdr">
          <span>Workspace Channels</span>
          <Badge variant={isConnected ? 'success' : 'warning'}>
            {isConnected ? 'LIVE' : 'OFFLINE'}
          </Badge>
        </div>

        <div className="conv-list">
          {filteredRooms.map((r) => {
            const roomMsgs = messagesByTopic[r.topic] ?? [];
            const lastMsg = roomMsgs[roomMsgs.length - 1];
            const queuedInRoom = outbox.filter((o) => o.topic === r.topic).length;
            const isActive = r.topic === topic;
            const lastSender = lastMsg
              ? usersById[lastMsg.sender_id]?.display_name || lastMsg.sender_name || lastMsg.sender_id
              : '';

            return (
              <button
                key={r.topic}
                type="button"
                className={`conv-item ${isActive ? 'conv-item--active' : ''}`}
                onClick={() => navigate(`/chat/${encodeURIComponent(r.topic)}`)}
              >
                <div className="conv-item__avatar-wrap">
                  <Avatar name={r.name} size="md" />
                  {roomMsgs.length > 0 && (
                    <span className="conv-item__unread">{roomMsgs.length}</span>
                  )}
                </div>

                <div className="conv-item__body">
                  <div className="conv-item__top">
                    <span className="conv-item__name">#{r.name}</span>
                    <span className="conv-item__time">
                      {lastMsg ? formatTime(lastMsg.timestamp) : formatTime(r.created_at)}
                    </span>
                  </div>
                  <div className="conv-item__preview">
                    {lastMsg
                      ? `${lastSender}: ${lastMsg.text || lastMsg.attachment?.filename || 'Attachment'}`
                      : r.description || 'No messages yet'}
                  </div>
                  <div className="conv-item__bottom">
                    {queuedInRoom > 0 ? (
                      <Badge variant="warning">{queuedInRoom} queued</Badge>
                    ) : isActive ? (
                      <Badge variant="primary">Active Room</Badge>
                    ) : (
                      <Badge variant="default">Synced</Badge>
                    )}
                    {lastMsg && (
                      <Avatar name={lastSender} size="sm" status={presenceMap[lastMsg.sender_id]?.status} />
                    )}
                  </div>
                </div>
              </button>
            );
          })}
          {filteredRooms.length === 0 && (
            <div style={{ padding: 16, fontSize: 12, color: 'var(--text-muted)', textAlign: 'center' }}>
              No matching channels.
            </div>
          )}
        </div>
      </aside>

      {/* Column 3: Active Conversation Thread */}
      <div className="chat-main">
        <div className="chat-header">
          <div>
            <div className="chat-header__title">#{activeRoom?.name || topic}</div>
            <div className="chat-header__sub">
              {activeRoom?.description || 'Real-time topic stream'} • {messages.length} messages
            </div>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            {!isConnected && <Badge variant="warning">Offline Queue Active</Badge>}
            <StatusSelector />
          </div>
        </div>

        <div className="chat-messages">
          {hasMore && messages.length >= 20 && (
            <div style={{ textAlign: 'center' }}>
              <Button variant="ghost" size="sm" loading={loadingOlder} onClick={() => void handleLoadOlder()}>
                Load earlier messages
              </Button>
            </div>
          )}

          <div className="thread-hero">
            <Avatar name={activeRoom?.name || topic} size="lg" />
            <h2 className="thread-hero__title">#{activeRoom?.name || topic}</h2>
            <div className="thread-hero__sub">
              Channel by @{activeRoom?.created_by || 'system'} • Real-Time WebSocket &amp; Local-First Sync
            </div>
          </div>

          <div className="day-divider">
            <span className="day-divider__pill">Today</span>
          </div>

          {!rooms.length && messages.length === 0 ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
              <Skeleton height="52px" />
              <Skeleton height="52px" />
            </div>
          ) : messages.length === 0 ? (
            <div style={{ color: 'var(--text-muted)', textAlign: 'center', marginTop: 24, fontSize: 13 }}>
              This is the very beginning of #{topic}. Send a message below!
            </div>
          ) : (
            messages.map((m) => {
              const isOwn = m.sender_id === user?.id;
              const senderPresence = presenceMap[m.sender_id]?.status;
              const mediaUrl = m.attachment ? `${APP_CONFIG.apiBaseUrl}${m.attachment.url}` : undefined;
              const isImage = m.attachment?.mime_type?.startsWith('image/');

              return (
                <div key={m.message_id} className={`msg-row ${isOwn ? 'msg-row--own' : ''}`}>
                  <Avatar name={m.sender_name || m.sender_id} size="sm" status={senderPresence} />
                  <div className="msg-col">
                    <div className="msg-sender">{m.sender_name || m.sender_id}</div>
                    <div className="msg-bubble">
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
                    </div>

                    <div className="msg-footer">
                      <span>{formatTime(m.timestamp)}</span>
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
                            <Smile size={12} />
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
                                boxShadow: 'var(--shadow)',
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
                        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 3 }}>
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
            <span>{typingList.map((t) => t.display_name || t.user_id).join(', ')} is typing...</span>
          )}
        </div>

        <form className="composer-wrap" onSubmit={(e) => void handleSend(e)}>
          {pendingAttachment && (
            <div className="msg-attachment" style={{ marginBottom: 8, justifyContent: 'space-between' }}>
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

          <div className="composer-pill" style={{ position: 'relative' }}>
            <input
              ref={fileInputRef}
              type="file"
              style={{ display: 'none' }}
              onChange={(e) => void handleFileChange(e)}
            />
            <button
              type="button"
              className="icon-btn"
              disabled={uploading}
              onClick={() => fileInputRef.current?.click()}
              aria-label="Upload attachment"
              title="Attach file"
            >
              <Paperclip size={17} />
            </button>

            <input
              className="composer__input"
              value={text}
              onChange={(e) => handleTypingChange(e.target.value)}
              placeholder={`Type your message in #${topic}...`}
            />

            <button
              type="button"
              className="icon-btn"
              title="Mention teammate"
              aria-label="Mention teammate"
              onClick={() => setText((prev) => `${prev}${prev.endsWith(' ') || !prev ? '' : ' '}@`)}
            >
              <AtSign size={16} />
            </button>

            <button
              type="button"
              className="icon-btn"
              title="Insert emoji"
              aria-label="Insert emoji"
              onClick={() => setComposerEmojiOpen((o) => !o)}
            >
              <Smile size={16} />
            </button>

            {composerEmojiOpen && (
              <div
                style={{
                  position: 'absolute',
                  right: 48,
                  bottom: '115%',
                  display: 'flex',
                  gap: 4,
                  background: 'var(--bg-surface)',
                  border: '1px solid var(--border-color)',
                  borderRadius: 999,
                  padding: '4px 8px',
                  boxShadow: 'var(--shadow)',
                  zIndex: 25,
                }}
              >
                {QUICK_EMOJIS.map((em) => (
                  <button
                    key={em}
                    type="button"
                    className="icon-btn"
                    style={{ padding: 4 }}
                    onClick={() => {
                      setText((prev) => `${prev}${em}`);
                      setComposerEmojiOpen(false);
                    }}
                  >
                    {em}
                  </button>
                ))}
              </div>
            )}

            <button type="submit" className="send-fab" aria-label="Send message">
              <Send size={16} />
            </button>
          </div>
        </form>
      </div>

      {/* Column 4: Right Context Inspector */}
      <aside className="inspector-pane" aria-label="Channel Inspector & Team Presence">
        <div className="inspector-section">
          <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 11, color: 'var(--text-muted)' }}>
            <span>#{topic}</span>
            <span>{activeRoom ? formatRelativeTime(activeRoom.created_at) : 'Active'}</span>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <Avatar name={activeRoom?.name || topic} size="md" />
            <div>
              <div style={{ fontWeight: 700, fontSize: 14 }}>{activeRoom?.name || topic}</div>
              <Badge variant={isConnected ? 'primary' : 'warning'}>
                {isConnected ? 'REAL-TIME ACTIVE' : 'OFFLINE MODE'}
              </Badge>
            </div>
          </div>

          <div className="info-kv">
            <Hash size={14} className="info-kv__icon" />
            <div>
              <div className="info-kv__label">Topic Channel</div>
              <div className="info-kv__val">#{topic}</div>
            </div>
          </div>

          <div className="info-kv">
            <Wifi size={14} className="info-kv__icon" />
            <div>
              <div className="info-kv__label">Transport State</div>
              <div className="info-kv__val">{socketState}</div>
            </div>
          </div>

          <div className="info-kv">
            <ShieldCheck size={14} className="info-kv__icon" />
            <div>
              <div className="info-kv__label">Sync Checkpoint</div>
              <div className="info-kv__val">
                Event #{lastEventId} • {lastSyncAt ? formatRelativeTime(lastSyncAt) : 'Local Cache'}
              </div>
            </div>
          </div>
        </div>

        <div className="inspector-section">
          <div className="inspector-section__title">
            <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
              <Users size={14} /> Team Presence
            </span>
            <Badge variant="success">{activeUsersCount} online</Badge>
          </div>

          <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
            {directoryUsers.map((u) => {
              const rec = presenceMap[u.id];
              const status: PresenceStatus = rec?.status ?? 'OFFLINE';
              const custom = rec?.custom_status ?? u.custom_status;
              return (
                <div key={u.id} className="user-row">
                  <Avatar name={u.display_name} src={u.avatar_url} size="sm" status={status} />
                  <div style={{ minWidth: 0, flex: 1 }}>
                    <div
                      style={{
                        fontSize: 12.5,
                        fontWeight: 600,
                        overflow: 'hidden',
                        textOverflow: 'ellipsis',
                        whiteSpace: 'nowrap',
                      }}
                    >
                      {u.display_name}
                    </div>
                    <div
                      style={{
                        fontSize: 11,
                        color: 'var(--text-muted)',
                        overflow: 'hidden',
                        textOverflow: 'ellipsis',
                        whiteSpace: 'nowrap',
                      }}
                    >
                      {custom || status.toLowerCase()}
                    </div>
                  </div>
                  <StatusDot status={status} size="sm" />
                </div>
              );
            })}
          </div>
        </div>

        <div className="inspector-section">
          <div className="inspector-section__title">
            <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
              <Activity size={14} /> Sync Activity
            </span>
            <button
              type="button"
              className="icon-btn"
              title="Run Delta Sync"
              onClick={() => {
                void syncEngine.flushOutbox();
                void syncEngine.runDeltaSync();
              }}
            >
              <RefreshCw size={13} />
            </button>
          </div>

          <div style={{ fontSize: 12, color: 'var(--text-secondary)', display: 'flex', flexDirection: 'column', gap: 6 }}>
            <div>Outbox Queue: <strong>{outbox.length} pending</strong></div>
            <div>Cached Messages: <strong>{messages.length} in #{topic}</strong></div>
          </div>
        </div>

        <div className="inspector-section" style={{ borderBottom: 'none' }}>
          <div className="inspector-section__title">
            <span>Shared Media</span>
            <span style={{ fontSize: 11, color: 'var(--text-muted)' }}>{sharedMedia.length}</span>
          </div>
          {sharedMedia.length === 0 ? (
            <div style={{ fontSize: 12, color: 'var(--text-muted)' }}>
              No files shared in #{topic} yet.
            </div>
          ) : (
            <div className="media-grid">
              {sharedMedia.slice(0, 6).map((att) => {
                const url = `${APP_CONFIG.apiBaseUrl}${att.url}`;
                const isImg = att.mime_type.startsWith('image/');
                return (
                  <a key={att.id} href={url} target="_blank" rel="noreferrer" className="media-tile" title={att.filename}>
                    {isImg ? (
                      <img src={url} alt={att.filename} />
                    ) : (
                      <>
                        <FileText size={16} />
                        <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', maxWidth: '100%', whiteSpace: 'nowrap' }}>
                          {att.filename}
                        </span>
                      </>
                    )}
                  </a>
                );
              })}
            </div>
          )}
        </div>
      </aside>

      <NewRoomModal open={newRoomOpen} onClose={() => setNewRoomOpen(false)} />
    </div>
  );
};
