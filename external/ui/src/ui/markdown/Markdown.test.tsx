import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, expect, test } from 'vitest';
import { Markdown } from './Markdown';

const stylesPath = join(dirname(fileURLToPath(import.meta.url)), '..', '..', 'styles.css');

afterEach(() => cleanup());

test('coddy-skill links render as chip spans', () => {
  render(<Markdown text="Try [/demo](coddy-skill:demo) now." />);
  const chip = screen.getByTestId('coddy-skill-span');
  expect(chip).toHaveAttribute('data-skill-name', 'demo');
  expect(chip.textContent).toBe('/demo');
});

test('fenced code block wrapper keeps symmetric vertical margin in styles', () => {
  const css = readFileSync(stylesPath, 'utf8');
  const m = /\.md-code\s*\{[^}]*\}/.exec(css);
  expect(m).not.toBeNull();
  expect(m![0]).toMatch(/margin:\s*12px\s+0/);
});

test('fenced code without language still shows Copy control', () => {
  const md = ['```', 'AGENTS.md', '```'].join('\n');
  render(<Markdown text={md} />);
  expect(screen.getByRole('button', { name: /copy code/i })).toBeInTheDocument();
});
