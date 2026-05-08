import React from 'react';
import { afterEach, expect, test } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import { MessageList } from './MessageList';
import type { TranscriptItem } from '../chat/types';

afterEach(() => cleanup());

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

test('tool call message uses compact spacing wrapper', () => {
  const items: TranscriptItem[] = [
    {
      id: 't1',
      type: 'tool_call',
      toolCallId: 'call_1',
      title: 'write_file',
      kind: 'write',
      status: 'completed',
      argsText: '{"path":"a.txt","content":"hi"}',
      resultText: 'OK',
    },
    { id: 'r1', type: 'thinking', status: 'completed', content: 'thinking', durationMs: 10 },
  ];

  render(<MessageList items={items} />);

  const wrapper = screen.getByText('write_file').closest('.msg');
  expect(wrapper).toBeTruthy();
  expect(wrapper).toHaveClass('msg-compact');

  // Ensure thinking row is a direct sibling so CSS `.msg-tools + .thinking-row` applies.
  expect(wrapper?.nextElementSibling).toHaveClass('thinking-row');
});
