import fs from "node:fs/promises";
import { deflateRawSync } from "node:zlib";
function crc32(bytes) {
  let crc = 0xffffffff;
  for (const byte of bytes) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit++)
      crc = (crc >>> 1) ^ (crc & 1 ? 0xedb88320 : 0);
  }
  return (crc ^ 0xffffffff) >>> 0;
}
// Small deterministic ZIP writer for the flat extension bundles. No runtime
// packages or remote build scripts are needed to produce installable archives.
export async function zipDirectory(directory, destination) {
  const local = [],
    central = [];
  let offset = 0;
  for (const name of (await fs.readdir(directory)).sort()) {
    const bytes = await fs.readFile(`${directory}/${name}`),
      compressed = deflateRawSync(bytes),
      filename = Buffer.from(name),
      crc = crc32(bytes);
    const header = Buffer.alloc(30);
    header.writeUInt32LE(0x04034b50);
    header.writeUInt16LE(20, 4);
    header.writeUInt16LE(8, 8);
    header.writeUInt16LE(33, 12);
    header.writeUInt32LE(crc, 14);
    header.writeUInt32LE(compressed.length, 18);
    header.writeUInt32LE(bytes.length, 22);
    header.writeUInt16LE(filename.length, 26);
    local.push(header, filename, compressed);
    const entry = Buffer.alloc(46);
    entry.writeUInt32LE(0x02014b50);
    entry.writeUInt16LE(20, 4);
    entry.writeUInt16LE(20, 6);
    entry.writeUInt16LE(8, 10);
    entry.writeUInt16LE(33, 14);
    entry.writeUInt32LE(crc, 16);
    entry.writeUInt32LE(compressed.length, 20);
    entry.writeUInt32LE(bytes.length, 24);
    entry.writeUInt16LE(filename.length, 28);
    entry.writeUInt32LE(offset, 42);
    central.push(entry, filename);
    offset += header.length + filename.length + compressed.length;
  }
  const directoryBytes = Buffer.concat(central),
    end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50);
  end.writeUInt16LE(central.length / 2, 8);
  end.writeUInt16LE(central.length / 2, 10);
  end.writeUInt32LE(directoryBytes.length, 12);
  end.writeUInt32LE(offset, 16);
  await fs.writeFile(
    destination,
    Buffer.concat([...local, directoryBytes, end]),
  );
}
