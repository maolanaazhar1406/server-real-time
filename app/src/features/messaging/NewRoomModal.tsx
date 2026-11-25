import React, { useState } from 'react';
import { useRouter } from '@/core/router/router';
import { Button, Input, Modal, useToast } from '@/shared/ui';
import { slugifyTopic } from '@/shared/utils/validators';
import { messagingService } from './messagingService';

export const NewRoomModal: React.FC<{ open: boolean; onClose: () => void }> = ({ open, onClose }) => {
  const { navigate } = useRouter();
  const { pushToast } = useToast();
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const topic = slugifyTopic(name);
    if (!topic) return;
    setLoading(true);
    try {
      const room = await messagingService.createRoom(topic, name, description);
      pushToast({ title: `Room #${room.topic} created`, variant: 'success' });
      setName('');
      setDescription('');
      onClose();
      navigate(`/chat/${room.topic}`);
    } catch (err) {
      pushToast({
        title: 'Could not create room',
        description: err instanceof Error ? err.message : undefined,
        variant: 'error',
      });
    } finally {
      setLoading(false);
    }
  };

  return (
    <Modal open={open} onClose={onClose} title="Create New Channel">
      <form onSubmit={(e) => void handleSubmit(e)} style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
        <Input
          label="Channel Name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="design-review"
          required
        />
        <Input
          label="Description"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="Topic description"
        />
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
          <Button type="button" variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" loading={loading}>
            Create Channel
          </Button>
        </div>
      </form>
    </Modal>
  );
};
