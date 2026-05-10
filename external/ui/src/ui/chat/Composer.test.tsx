import React, { useState } from 'react';
import { afterEach, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { expect, test } from 'vitest';
import { Composer } from './Composer';

afterEach(() => cleanup());

function renderComposer(opts: { isEmpty: boolean }) {
  return render(
    <Composer
      value=""
      isEmpty={opts.isEmpty}
      mode="agent"
      modes={['agent', 'plan']}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
}

function renderComposerWithLlm(opts: { isEmpty: boolean }) {
  return render(
    <Composer
      value=""
      isEmpty={opts.isEmpty}
      mode="agent"
      modes={['agent', 'plan']}
      llmModels={['openai/gpt-4o-mini', 'openai/gpt-4o']}
      llmModel="openai/gpt-4o-mini"
      onLlmModelChange={() => {}}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
}

test('mode menu opens down on start screen', () => {
  renderComposer({ isEmpty: true });

  fireEvent.click(screen.getByRole('button', { name: 'Mode' }));

  const menu = screen.getByRole('menu');
  expect(menu).toHaveClass('opens-down');
});

test('mode menu opens up in active chat composer', () => {
  renderComposer({ isEmpty: false });

  fireEvent.click(screen.getByRole('button', { name: 'Mode' }));

  const menu = screen.getByRole('menu');
  expect(menu).toHaveClass('opens-up');
});

test('yaml model menu opens down on start screen when backends exist', () => {
  renderComposerWithLlm({ isEmpty: true });

  fireEvent.click(screen.getByRole('button', { name: 'Model' }));

  const menu = screen.getByRole('menu');
  expect(menu).toHaveClass('opens-down');
});

test('yaml model menu opens up in active chat composer', () => {
  renderComposerWithLlm({ isEmpty: false });

  fireEvent.click(screen.getByRole('button', { name: 'Model' }));

  const menu = screen.getByRole('menu');
  expect(menu).toHaveClass('opens-up');
});

test('send play disabled when input empty', () => {
  renderComposer({ isEmpty: true });
  expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled();
});

test('send play enabled when draft has text', () => {
  render(
    <Composer
      value="hi"
      isEmpty={true}
      mode="agent"
      modes={['agent', 'plan']}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  expect(screen.getByRole('button', { name: 'Send' })).not.toBeDisabled();
});

test('composer highlights only the active slash draft at caret', () => {
  const s = 'asdfasf /find-skills asdfasdf';
  render(
    <Composer
      value={s}
      isEmpty={false}
      mode="agent"
      modes={['agent', 'plan']}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  const ta = screen.getByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement;
  const caret = s.indexOf('/') + '/find-skil'.length;
  ta.focus();
  ta.setSelectionRange(caret, caret);
  fireEvent.select(ta);

  const chip = screen.getByTestId('composer-skill-chip');
  expect(chip).toHaveTextContent('/find-skil');
});

test('no slash chip and no menu after API returns zero commands for prefix', async () => {
  const fetchMock = vi.fn().mockResolvedValue({
    ok: true,
    json: async () => ({ items: [], has_more: false, page: 1 }),
  });
  vi.stubGlobal('fetch', fetchMock);

  function Harness() {
    const [value, setValue] = useState('');
    return (
      <Composer
        value={value}
        isEmpty={false}
        mode="agent"
        modes={['agent', 'plan']}
        onModeChange={() => {}}
        onChange={setValue}
        onSend={() => {}}
      />
    );
  }

  render(<Harness />);
  const ta = screen.getByRole('textbox', { name: 'Message' });
  fireEvent.change(ta, { target: { value: '/as', selectionStart: 3, selectionEnd: 3 } });

  await waitFor(() => {
    expect(fetchMock).toHaveBeenCalled();
  });
  await waitFor(() => {
    expect(screen.queryByTestId('composer-skill-chip')).toBeNull();
  });
  expect(screen.queryByRole('listbox', { name: 'Slash commands' })).toBeNull();

  vi.unstubAllGlobals();
});

test('extending a no-match prefix does not reopen slash menu or refetch', async () => {
  const fetchMock = vi.fn().mockResolvedValue({
    ok: true,
    json: async () => ({ items: [], has_more: false, page: 1 }),
  });
  vi.stubGlobal('fetch', fetchMock);

  function Harness() {
    const [value, setValue] = useState('');
    return (
      <Composer
        value={value}
        isEmpty={false}
        mode="agent"
        modes={['agent', 'plan']}
        onModeChange={() => {}}
        onChange={setValue}
        onSend={() => {}}
      />
    );
  }

  render(<Harness />);
  const ta = screen.getByRole('textbox', { name: 'Message' });
  fireEvent.change(ta, { target: { value: '/adf', selectionStart: 4, selectionEnd: 4 } });
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
  fireEvent.change(ta, {
    target: { value: '/adfadsfgaf', selectionStart: '/adfadsfgaf'.length, selectionEnd: '/adfadsfgaf'.length },
  });
  await new Promise((r) => setTimeout(r, 250));
  expect(fetchMock).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole('listbox', { name: 'Slash commands' })).toBeNull();
  expect(screen.queryByTestId('composer-skill-chip')).toBeNull();

  vi.unstubAllGlobals();
});

test('generating shows stop and calls onStop', () => {
  let stopped = false;
  render(
    <Composer
      value=""
      isEmpty={true}
      generating={true}
      mode="agent"
      modes={['agent', 'plan']}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
      onStop={() => {
        stopped = true;
      }}
    />,
  );
  const b = screen.getByRole('button', { name: 'Stop generation' });
  expect(b).not.toBeDisabled();
  fireEvent.click(b);
  expect(stopped).toBe(true);
});

test('context tooltip percent and Max context follow cap when model max changes', () => {
  const usage = { inputTokens: 800, outputTokens: 200, totalTokens: 1000 };
  const { rerender } = render(
    <Composer
      value=""
      isEmpty={false}
      mode="agent"
      modes={['agent', 'plan']}
      tokenUsage={usage}
      contextPct={1.0}
      maxContextTokens={100000}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  const tip = () => screen.getByRole('tooltip').textContent ?? '';
  expect(tip()).toMatch(/1\.0% context used/);
  expect(tip()).toMatch(/Max context 100000/);

  rerender(
    <Composer
      value=""
      isEmpty={false}
      mode="agent"
      modes={['agent', 'plan']}
      tokenUsage={usage}
      contextPct={10.0}
      maxContextTokens={10000}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  expect(tip()).toMatch(/10\.0% context used/);
  expect(tip()).toMatch(/Max context 10000/);
});

