import { describe, expect, it } from "vitest";
import { describeAgent, fileExt, fileKind, formatBytes, formatEta, isFinalStatus, previewable, relativeTime } from "./format";

describe("format", () => {
  it("formats bytes", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(1536)).toBe("1.5 KB");
    expect(formatBytes(20 * 1024 * 1024)).toBe("20 MB");
    expect(formatBytes(-1)).toBe("—");
  });
  it("formats relative time against a given clock", () => {
    const now = 1_000_000_000_000;
    expect(relativeTime(now + 2 * 3600_000, now)).toMatch(/2 hours/);
    expect(relativeTime(now - 5000, now)).toBe("just now");
  });
  it("classifies files", () => {
    expect(fileKind("image/png")).toBe("image");
    expect(fileKind("application/octet-stream", "a.docx")).toBe("doc");
    expect(fileExt("archive.tar.gz")).toBe("GZ");
    expect(fileExt("README")).toBe("FILE");
  });
  it("never previews script-capable types", () => {
    expect(previewable("text/html")).toBe(false);
    expect(previewable("image/svg+xml")).toBe(false);
    expect(previewable("application/pdf")).toBe(true);
  });
  it("knows final states and ETA", () => {
    expect(isFinalStatus("completed")).toBe(true);
    expect(isFinalStatus("interrupted")).toBe(false);
    expect(formatEta(30)).toBe("30s left");
  });
});

describe("describeAgent", () => {
  it("labels common browsers and the app", () => {
    expect(describeAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36")).toBe("Chrome on Windows");
    expect(describeAgent("Mozilla/5.0 (Windows NT 10.0) AppleWebKit/537.36 Chrome/130.0 Safari/537.36 Edg/130.0")).toBe("Edge on Windows");
    expect(describeAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0; rv:131.0) Gecko/20100101 Firefox/131.0")).toBe("Firefox on macOS");
    expect(describeAgent("okhttp/4.12.0")).toBe("Ferry app");
    expect(describeAgent("")).toBe("Browser");
  });
});
