import { describe, expect, it } from "vitest";
import { appendSample, cpuLevel, HISTORY_LENGTH, isSample, loadLevel, memLevel, shortBytes, type SysSample } from "./sysstats";

const sample = (cpu: number): SysSample => ({ at: "t", cpu, cores: 16, mem_used: 1, mem_total: 2, load1: 1, load5: 1, load15: 1, disk_free: 0, disk_total: 0, scope: "host", working: 0 });

describe("sysstats", () => {
  it("keeps a minute of samples", () => {
    let stats = undefined;
    for (let i = 0; i < HISTORY_LENGTH + 4; i++) stats = appendSample(stats, sample(i));
    expect(stats?.history).toHaveLength(HISTORY_LENGTH);
    expect(stats?.latest?.cpu).toBe(HISTORY_LENGTH + 3);
  });

  it("colours load against the core count: 63 on 16 cores is saturated", () => {
    expect(loadLevel(63, 16)).toBe("saturated");
    expect(loadLevel(20, 16)).toBe("busy");
    expect(loadLevel(4, 16)).toBe("ok");
    expect(cpuLevel(95)).toBe("saturated");
    expect(memLevel(97, 100)).toBe("saturated");
  });

  it("recognises a sample and formats bytes", () => {
    expect(isSample(sample(1))).toBe(true);
    expect(isSample({ cpu: 1 })).toBe(false);
    expect(shortBytes(1536 << 20)).toBe("1.5G");
    expect(shortBytes(512)).toBe("512B");
  });
});
