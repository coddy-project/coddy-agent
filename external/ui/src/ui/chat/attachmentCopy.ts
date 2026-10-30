/**
 * A copy of an attachment that lives in memory. The File a drop, a paste or a
 * file picker hands over can be no more than a path on disk, and a screenshot
 * tool deletes its temporary file at the next capture: from then on the picture
 * cannot be shown or sent. Attaching takes the bytes while they are there.
 * Rejects when the browser can no longer read the file. An environment without
 * `arrayBuffer` keeps the file as it is.
 */
export async function inMemoryCopy(file: File): Promise<File> {
  if (typeof file.arrayBuffer !== "function") return file;
  const bytes = await file.arrayBuffer();
  return new File([bytes], file.name, {
    type: file.type,
    lastModified: file.lastModified,
  });
}
