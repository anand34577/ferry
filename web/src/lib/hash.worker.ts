// SHA-256 of a file off the main thread, so hashing large uploads never makes the page stutter.
import { createSHA256 } from "hash-wasm";

self.onmessage = async (e: MessageEvent<Blob>) => {
  try {
    const h = await createSHA256();
    h.init();
    const file = e.data;
    const step = 8 * 1024 * 1024;
    for (let off = 0; off < file.size; off += step) h.update(new Uint8Array(await file.slice(off, off + step).arrayBuffer()));
    self.postMessage({ hash: h.digest("hex") });
  } catch (err) {
    self.postMessage({ error: String(err) });
  }
};
