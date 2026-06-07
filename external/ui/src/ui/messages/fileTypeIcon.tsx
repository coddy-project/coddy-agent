import type { ReactNode } from "react";

/** Returns a short SVG icon and label for a given MIME type or file name. */
export function fileTypeIcon(mimeType: string, fileName: string): { svg: ReactNode; label: string } {
  const mt = (mimeType || "").toLowerCase();
  const ext = (fileName.split(".").pop() || "").toLowerCase();

  if (mt.startsWith("image/") || ["png", "jpg", "jpeg", "gif", "webp", "svg", "bmp", "ico"].includes(ext)) {
    return { label: "Image", svg: <ImageIcon /> };
  }
  if (mt.startsWith("video/") || ["mp4", "mov", "avi", "mkv", "webm", "flv"].includes(ext)) {
    return { label: "Video", svg: <VideoIcon /> };
  }
  if (mt.startsWith("audio/") || ["mp3", "wav", "ogg", "flac", "aac", "m4a"].includes(ext)) {
    return { label: "Audio", svg: <AudioIcon /> };
  }
  if (mt === "application/pdf" || ext === "pdf") {
    return { label: "PDF", svg: <PdfIcon /> };
  }
  if (
    mt.startsWith("text/") ||
    ["txt", "md", "csv", "log", "yaml", "yml", "json", "xml", "html", "css", "js", "ts", "tsx", "jsx", "py", "go", "rs", "java", "c", "cpp", "h"].includes(ext)
  ) {
    return { label: "Text", svg: <TextIcon /> };
  }
  if (["zip", "tar", "gz", "rar", "7z", "bz2"].includes(ext) || mt.includes("zip") || mt.includes("archive")) {
    return { label: "Archive", svg: <ArchiveIcon /> };
  }
  return { label: "File", svg: <FileIcon /> };
}

function ImageIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <rect x="1.5" y="2.5" width="13" height="11" rx="1.5" stroke="currentColor" strokeWidth="1.25" />
      <circle cx="5.5" cy="6" r="1.25" fill="currentColor" />
      <path d="M1.5 11l3.5-3.5 2.5 2.5 2-2 4 4" stroke="currentColor" strokeWidth="1.25" strokeLinejoin="round" />
    </svg>
  );
}

function VideoIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <rect x="1.5" y="3" width="9" height="10" rx="1.5" stroke="currentColor" strokeWidth="1.25" />
      <path d="M10.5 6l4-2v8l-4-2V6z" stroke="currentColor" strokeWidth="1.25" strokeLinejoin="round" />
    </svg>
  );
}

function AudioIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <path d="M8 2v12M5 4.5v7M11 4.5v7M2.5 6.5v3M13.5 6.5v3" stroke="currentColor" strokeWidth="1.25" strokeLinecap="round" />
    </svg>
  );
}

function PdfIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <path d="M3 2h7l3 3v9a1 1 0 01-1 1H3a1 1 0 01-1-1V3a1 1 0 011-1z" stroke="currentColor" strokeWidth="1.25" />
      <path d="M10 2v3h3" stroke="currentColor" strokeWidth="1.25" strokeLinejoin="round" />
      <text x="4" y="12" fontSize="4.5" fill="currentColor" fontWeight="700">PDF</text>
    </svg>
  );
}

function TextIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <path d="M3 2h7l3 3v9a1 1 0 01-1 1H3a1 1 0 01-1-1V3a1 1 0 011-1z" stroke="currentColor" strokeWidth="1.25" />
      <path d="M10 2v3h3" stroke="currentColor" strokeWidth="1.25" strokeLinejoin="round" />
      <path d="M5 8h6M5 10.5h4" stroke="currentColor" strokeWidth="1.25" strokeLinecap="round" />
    </svg>
  );
}

function ArchiveIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <rect x="1.5" y="4" width="13" height="9.5" rx="1.5" stroke="currentColor" strokeWidth="1.25" />
      <path d="M1.5 7.5h13" stroke="currentColor" strokeWidth="1.25" />
      <rect x="1.5" y="2.5" width="13" height="2" rx="1" stroke="currentColor" strokeWidth="1.25" />
      <path d="M6.5 6v3M9.5 6v3" stroke="currentColor" strokeWidth="1.25" strokeLinecap="round" />
    </svg>
  );
}

function FileIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <path d="M3 2h7l3 3v9a1 1 0 01-1 1H3a1 1 0 01-1-1V3a1 1 0 011-1z" stroke="currentColor" strokeWidth="1.25" />
      <path d="M10 2v3h3" stroke="currentColor" strokeWidth="1.25" strokeLinejoin="round" />
    </svg>
  );
}
