import React from 'react';
import { afterEach } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
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

