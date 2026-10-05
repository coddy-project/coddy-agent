export interface OpenFileRequest {
  path: string;
  line?: number | undefined;
}
const listeners = new Set<(request: OpenFileRequest) => void>();
export function openWorkspaceFile(path = "", line?: number): void {
  for (const listener of listeners) listener({ path, line });
}
export function onOpenWorkspaceFile(
  listener: (request: OpenFileRequest) => void,
): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}
