/**
 * The glyphs of the windows over a chat - Files and Edits - so both heads and
 * both trees are drawn with the same strokes.
 */

const iconProps = {
  width: 16,
  height: 16,
  viewBox: "0 0 16 16",
  fill: "none",
  stroke: "currentColor",
  strokeWidth: 1.4,
  strokeLinecap: "round" as const,
  strokeLinejoin: "round" as const,
  "aria-hidden": true,
};

export function IconTree() {
  return (
    <svg {...iconProps}>
      <path d="M2.5 3.5h1M2.5 8h1M2.5 12.5h1M6 3.5h7.5M6 8h7.5M6 12.5h7.5" />
    </svg>
  );
}

export function IconMore() {
  return (
    <svg {...iconProps}>
      <circle cx="8" cy="3.25" r="0.6" fill="currentColor" />
      <circle cx="8" cy="8" r="0.6" fill="currentColor" />
      <circle cx="8" cy="12.75" r="0.6" fill="currentColor" />
    </svg>
  );
}

export function IconExpand() {
  return (
    <svg {...iconProps}>
      <path d="M9.5 2.5h4v4M13.5 2.5 9 7M6.5 13.5h-4v-4M2.5 13.5 7 9" />
    </svg>
  );
}

export function IconRestore() {
  return (
    <svg {...iconProps}>
      <path d="M13.5 6.5h-4v-4M9.5 6.5 14 2M2.5 9.5h4v4M6.5 9.5 2 14" />
    </svg>
  );
}

export function IconSearch() {
  return (
    <svg {...iconProps} className="files-filter-icon">
      <circle cx="7" cy="7" r="4.25" />
      <path d="m10.25 10.25 3.25 3.25" />
    </svg>
  );
}

export function IconFolder() {
  return (
    <svg {...iconProps} width={28} height={28} className="files-empty-icon">
      <path d="M1.75 4.25c0-.83.67-1.5 1.5-1.5h3l1.5 1.75h5c.83 0 1.5.67 1.5 1.5v6.25c0 .83-.67 1.5-1.5 1.5h-9.5c-.83 0-1.5-.67-1.5-1.5z" />
    </svg>
  );
}

export function IconFolderSmall() {
  return (
    <svg {...iconProps}>
      <path d="M1.75 4.25c0-.83.67-1.5 1.5-1.5h3l1.5 1.75h5c.83 0 1.5.67 1.5 1.5v6.25c0 .83-.67 1.5-1.5 1.5h-9.5c-.83 0-1.5-.67-1.5-1.5z" />
    </svg>
  );
}

/** A file by what it holds: source, a document, a picture, anything else. */
export function IconFile(props: { path: string }) {
  const lower = props.path.toLowerCase();
  if (/\.(md|markdown|txt|rst)$/.test(lower)) {
    return (
      <svg {...iconProps}>
        <path d="M4 1.75h5.5L12.5 4.75v9.5H4z" />
        <path d="M6.25 7.5h4M6.25 10h4" />
      </svg>
    );
  }
  if (/\.(png|jpe?g|gif|webp|avif|svg|ico)$/.test(lower)) {
    return (
      <svg {...iconProps}>
        <rect x="2.25" y="2.75" width="11.5" height="10.5" rx="1.5" />
        <path d="m2.75 11.5 3.25-3.25 2.5 2.5 1.5-1.5 3 3" />
      </svg>
    );
  }
  if (
    /\.(go|ts|tsx|js|jsx|mjs|py|rs|java|kt|c|h|cc|cpp|cs|rb|php|sh|ya?ml|json|toml|css|html|sql|swift)$/.test(
      lower,
    ) ||
    /(^|\/)(makefile|dockerfile)$/.test(lower)
  ) {
    return (
      <svg {...iconProps}>
        <path d="M5.5 4.5 2 8l3.5 3.5M10.5 4.5 14 8l-3.5 3.5" />
      </svg>
    );
  }
  return (
    <svg {...iconProps}>
      <path d="M4 1.75h5.5L12.5 4.75v9.5H4z" />
      <path d="M9.5 1.75v3h3" />
    </svg>
  );
}
