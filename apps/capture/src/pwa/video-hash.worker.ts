import { readVideoChunk } from "./video-drafts";
import { hashVideoBlobs } from "./video-hash";

type HashRequest = { attemptId: string; chunkCount: number };

self.onmessage = async ({ data }: MessageEvent<HashRequest>) => {
  const { attemptId, chunkCount } = data;
  try {
    async function* chunks(): AsyncGenerator<Blob> {
      for (let sequence = 0; sequence < chunkCount; sequence += 1) {
        const blob = await readVideoChunk(attemptId, sequence);
        if (!blob) throw new Error(`Parte ${sequence} ausente; a gravação está incompleta.`);
        yield blob;
      }
    }
    self.postMessage({ attemptId, sha256: await hashVideoBlobs(chunks()) });
  } catch (error) {
    self.postMessage({ attemptId, error: error instanceof Error ? error.message : "Falha ao verificar vídeo." });
  }
};
