import React from 'react';
import { render, screen } from '@testing-library/react';
import { expect, test } from 'vitest';
import { MessageList } from './MessageList';
import type { TranscriptItem } from '../chat/types';

test('renders user, assistant, and tool call items', () => {
  const items: TranscriptItem[] = [
    { id: 'u1', type: 'user_message', content: 'Hello' },
    { id: 'a1', type: 'assistant_message', content: 'Hi' },
    {
      id: 't1',
      type: 'tool_call',
      toolCallId: 'call_1',
      title: 'read_file',
      kind: 'read',
      status: 'completed',
      argsText: '{"path":"a.txt"}',
      resultText: 'OK',
    },
  ];

  render(<MessageList items={items} />);

  expect(screen.getByText('Hello')).toBeInTheDocument();
  expect(screen.getByText('Hi')).toBeInTheDocument();
  expect(screen.getByText('read_file')).toBeInTheDocument();
  expect(screen.getByLabelText('Tool summary')).toBeInTheDocument();
});
