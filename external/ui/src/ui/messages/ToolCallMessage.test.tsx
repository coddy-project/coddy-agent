import React, { useCallback, useState } from 'react';
import { afterEach, expect, test, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { ToolCallMessage } from './ToolCallMessage';

afterEach(() => cleanup());

function openToolDetails() {
  fireEvent.click(screen.getByLabelText('Tool summary'));
}

test('truncated tool shows text link, fetches once, then Hide restores preview', async () => {
  const fetchSpy = vi.fn();
  function Harness() {
    const [full, setFull] = useState<string | undefined>();
    const onFetch = useCallback(async (id: string) => {
      fetchSpy(id);
      await Promise.resolve();
      setFull('full line 1\nfull line 2\nfull line 3');
    }, []);
    return (
      <ToolCallMessage
        toolCallId="tc-1"
        title="list_dir"
        kind="bash"
        status="completed"
        argsText="{}"
        resultText={`${'a\n'.repeat(18)}last preview line\n...`}
        fullResultText={full}
        resultWasTruncated
        durationMs={42}
        onFetchToolCallFull={onFetch}
      />
    );
  }
  render(<Harness />);
  openToolDetails();

  const pre = document.querySelector('.tool-result-pre');
  expect(pre?.textContent ?? '').toMatch(/\n\.\.\.\s*$/);
  expect(pre?.textContent?.split('\n').pop()?.trim()).toBe('...');

  const more = screen.getByTestId('tool-result-more-link');
  expect(more).toHaveTextContent('Load more results');
  expect(screen.getByLabelText('Tool result').className).toContain('tool-result-viewport--tall');
  expect(screen.getByLabelText('Tool result').className).toContain('tool-result-viewport--clip');

  fireEvent.click(more);
  await waitFor(() => expect(fetchSpy).toHaveBeenCalledWith('tc-1'));
  await waitFor(() => expect(screen.getByTestId('tool-result-hide-link')).toBeInTheDocument());
  expect(screen.getByText(/full line 3/)).toBeInTheDocument();
  expect(screen.getByLabelText('Tool result').className).toContain('tool-result-viewport--scroll');

  fireEvent.click(screen.getByTestId('tool-result-hide-link'));
  expect(screen.queryByTestId('tool-result-hide-link')).toBeNull();
  expect(screen.getByTestId('tool-result-more-link')).toBeInTheDocument();
  expect(screen.getByText(/last preview line/)).toBeInTheDocument();
  expect(screen.getByLabelText('Tool result').className).toContain('tool-result-viewport--clip');

  fireEvent.click(screen.getByTestId('tool-result-more-link'));
  await waitFor(() => expect(screen.getByTestId('tool-result-hide-link')).toBeInTheDocument());
  expect(fetchSpy).toHaveBeenCalledTimes(1);
});

test('no load-more row when preview is not truncated', () => {
  render(
    <ToolCallMessage
      toolCallId="tc-2"
      title="read_file"
      status="completed"
      resultText="short"
      durationMs={10}
      onFetchToolCallFull={vi.fn()}
    />,
  );
  openToolDetails();
  expect(screen.queryByTestId('tool-result-more-link')).toBeNull();
  expect(screen.getByLabelText('Tool result').className).not.toContain('tool-result-viewport--tall');
});

test('truncated tool does not show toggle without fetch handler', () => {
  render(
    <ToolCallMessage toolCallId="tc-3" title="run" status="completed" resultText="a\n..." resultWasTruncated />,
  );
  openToolDetails();
  expect(screen.queryByTestId('tool-result-more-link')).toBeNull();
});

test('summary matches thinking-row pattern: chevron, tool name, duration', () => {
  const { container } = render(
    <ToolCallMessage
      toolCallId="tc-4"
      title="glob"
      status="completed"
      resultText="ok"
      durationMs={125}
      onFetchToolCallFull={vi.fn()}
    />,
  );
  const row = container.querySelector('.thinking-row.coddy-tool-call-row');
  expect(row).toBeTruthy();
  expect(container.querySelector('.thinking-row.coddy-tool-call-row .thinking-chevron')).toBeTruthy();
  expect(screen.getByText('glob')).toBeInTheDocument();
  expect(container.querySelector('.thinking-dur')?.textContent).toBe('125ms');
});

test('in-progress tool shows ellipsis on label and elapsed from startedAtMs', () => {
  const t0 = Date.now() - 2500;
  const { container } = render(
    <ToolCallMessage toolCallId="tc-5" title="run_cmd" status="in_progress" startedAtMs={t0} argsText="{}" />,
  );
  expect(screen.getByText('run_cmd...')).toBeTruthy();
  const dur = container.querySelector('.thinking-dur')?.textContent ?? '';
  expect(dur).toMatch(/^\d+ms$|^\d/);
});
