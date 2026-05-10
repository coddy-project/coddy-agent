import React from 'react';
import { afterEach, expect, test } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import { MessageList } from './MessageList';
import type { TranscriptItem } from '../chat/types';

afterEach(() => cleanup());

test('renders system error notice', () => {
  const items: TranscriptItem[] = [
    { id: 'u1', type: 'user_message', content: 'Hello' },
    { id: 's1', type: 'system_notice', level: 'error', message: 'LLM error: context exceeded' },
  ];

  render(<MessageList items={items} />);

  expect(screen.getByRole('alert')).toBeInTheDocument();
  expect(screen.getByText('System')).toBeInTheDocument();
  expect(screen.getByText('LLM error: context exceeded')).toBeInTheDocument();
});

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

test('renders memory copilot foldout', () => {
  const items: TranscriptItem[] = [
    { id: 'u1', type: 'user_message', content: 'Hi' },
    {
      id: 'm1',
      type: 'memory_copilot',
      memoryRowId: 'mem-1',
      userTurnIndex: 1,
      recallStatus: 'completed',
      persistStatus: 'completed',
      recallText: '- fact',
      recallReasoning: '',
      persistText: '{"save":false,"reason":"No durable fact to persist."}',
      persistReasoning: '',
      recallDurationMs: 10,
      persistDurationMs: 5,
      persistSaved: false,
    },
  ];

  render(<MessageList items={items} />);

  expect(screen.getByTestId('memory-copilot-row')).toBeTruthy();
  expect(document.querySelector('.coddy-memory-recall')).toBeTruthy();
  expect(screen.getByText('fact')).toBeInTheDocument();
  expect(screen.getByText(/No durable fact to persist/)).toBeInTheDocument();
});

test('tool call message uses thinking-row wrapper next to thinking row', () => {
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

  const wrapper = screen.getByText('write_file').closest('.thinking-row');
  expect(wrapper).toBeTruthy();
  expect(wrapper).toHaveClass('coddy-tool-call-row');

  // Tool and thinking are sibling foldout rows (same stack rhythm as messages-inner gap).
  expect(wrapper?.nextElementSibling).toHaveClass('thinking-row');
});
