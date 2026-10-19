import { sha256 } from "@noble/hashes/sha2.js";

export const videoHashReadBytes = 1024 * 1024;

export async function hashVideoBlobs(blobs: AsyncIterable<Blob> | Iterable<Blob>): Promise<string> {
  const hash = sha256.create();
  for await (const blob of blobs) {
    for (let offset = 0; offset < blob.size; offset += videoHashReadBytes) {
      hash.update(new Uint8Array(await blob.slice(offset, offset + videoHashReadBytes).arrayBuffer()));
    }
  }
  return Array.from(hash.digest(), (byte) => byte.toString(16).padStart(2, "0")).join("");
}
