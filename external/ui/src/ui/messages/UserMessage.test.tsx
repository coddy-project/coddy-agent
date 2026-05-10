import React from 'react';
import { afterEach } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import { expect, test } from 'vitest';
import { UserMessage } from './UserMessage';

afterEach(() => cleanup());

test('user bubble chips plain slash commands for Markdown', () => {
  render(<UserMessage content="hi /demo there" />);
  expect(screen.getByTestId('coddy-skill-span')).toHaveTextContent('/demo');
});
