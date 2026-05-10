import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, expect, test } from 'vitest';
import { Markdown } from './Markdown';

afterEach(() => cleanup());

test('coddy-skill links render as chip spans', () => {
  render(<Markdown text="Try [/demo](coddy-skill:demo) now." />);
  const chip = screen.getByTestId('coddy-skill-span');
  expect(chip).toHaveAttribute('data-skill-name', 'demo');
  expect(chip.textContent).toBe('/demo');
});
