"use strict";
// 运行：node web/test/live.test.js
const assert = require("node:assert");
const L = require("../live.js");
let passed = 0;
function test(name, fn) {
  try {
    fn();
    passed++;
  } catch (e) {
    console.error("FAIL: " + name);
    throw e;
  }
}

test("parseSSE: 单事件", () => {
  const r = L.parseSSE('event: quota\ndata: {"a":1}\n\n');
  assert.deepStrictEqual(r.events, [{ event: "quota", data: '{"a":1}' }]);
  assert.strictEqual(r.rest, "");
});
test("parseSSE: 多事件", () => {
  const r = L.parseSSE("event: start\ndata: 1\n\nevent: progress\ndata: 2\n\nevent: done\ndata: 3\n\n");
  assert.deepStrictEqual(r.events.map((e) => e.event), ["start", "progress", "done"]);
  assert.deepStrictEqual(r.events.map((e) => e.data), ["1", "2", "3"]);
});
test("parseSSE: 半条不吞，补全后可解析", () => {
  const a = L.parseSSE("event: progress\ndata: {\"x\"");
  assert.strictEqual(a.events.length, 0);
  assert.strictEqual(a.rest, 'event: progress\ndata: {"x"');
  const b = L.parseSSE(a.rest + ":1}\n\nevent: do");
  assert.deepStrictEqual(b.events, [{ event: "progress", data: '{"x":1}' }]);
  assert.strictEqual(b.rest, "event: do");
});
test("parseSSE: 心跳注释被忽略", () => {
  const r = L.parseSSE(": connected\n\n: ping\n\nevent: quota\ndata: {}\n\n");
  assert.deepStrictEqual(r.events, [{ event: "quota", data: "{}" }]);
});
test("parseSSE: 多行 data 拼接；CRLF 兼容；无 event 默认 message", () => {
  const r = L.parseSSE("data: a\ndata: b\n\n");
  assert.deepStrictEqual(r.events, [{ event: "message", data: "a\nb" }]);
  const c = L.parseSSE("event: x\r\ndata: y\r\n\r\n");
  assert.deepStrictEqual(c.events, [{ event: "x", data: "y" }]);
});

test("reconnectDelay: 指数退避并封顶 30 秒", () => {
  assert.deepStrictEqual([0, 1, 2, 3, 4].map(L.reconnectDelay), [1000, 2000, 4000, 8000, 16000]);
  assert.strictEqual(L.reconnectDelay(5), 30000);
  assert.strictEqual(L.reconnectDelay(50), 30000);
  assert.strictEqual(L.reconnectDelay(-1), 1000);
});

test("mergeEventAccount: 命中则浅合并并保留旧字段", () => {
  const list = [
    { email: "a@x", org_uuid: "org-a", status: "active", quota: { type: "old" } },
    { email: "b@x", status: "active" },
  ];
  const ok = L.mergeEventAccount(list, { email: "a@x", status: "expired", quota: { type: "new" } });
  assert.strictEqual(ok, true);
  assert.strictEqual(list[0].org_uuid, "org-a");
  assert.strictEqual(list[0].status, "expired");
  assert.strictEqual(list[0].quota.type, "new");
  assert.deepStrictEqual(list[1], { email: "b@x", status: "active" });
});
test("mergeEventAccount: 未命中返回 false 且不改列表", () => {
  const list = [{ email: "a@x", status: "active" }];
  assert.strictEqual(L.mergeEventAccount(list, { email: "zzz@x", status: "expired" }), false);
  assert.strictEqual(L.mergeEventAccount(list, null), false);
  assert.strictEqual(L.mergeEventAccount(list, {}), false);
  assert.deepStrictEqual(list, [{ email: "a@x", status: "active" }]);
});

test("estimateMinutes", () => {
  assert.strictEqual(L.estimateMinutes(10, 3, 8), 1);
  assert.strictEqual(L.estimateMinutes(60, 3, 8), 6);
  assert.strictEqual(L.estimateMinutes(0, 3, 8), 1);
});

test("windowEstimate: 普通与跨午夜", () => {
  assert.deepStrictEqual(L.windowEstimate("07:00"), { start: "07:00", end: "12:00", nextDay: false });
  assert.deepStrictEqual(L.windowEstimate("22:30"), { start: "22:30", end: "03:30", nextDay: true });
  assert.deepStrictEqual(L.windowEstimate("6:05"), { start: "06:05", end: "11:05", nextDay: false });
});
test("windowEstimate: 非法输入返回 null", () => {
  for (const v of ["", null, "abc", "25:00", "12:61", "12"]) assert.strictEqual(L.windowEstimate(v), null);
});

// 北京时间 2026-10-14 12:00 = UTC 04:00
const NOW = Date.parse("2026-10-14T04:00:00Z");
test("scheduleLine: 未开启", () => {
  assert.strictEqual(L.scheduleLine({ enabled: false }, NOW), "定时检查未开启");
  assert.strictEqual(L.scheduleLine(null, NOW), "定时检查未开启");
});
test("scheduleLine: 有上次结果，下次是明天", () => {
  const s = { enabled: true, last_run_at: "2026-10-13T23:00:00Z", total: 15, ok: 14, failed: 1, skipped: 0, next_run_at: "2026-10-14T23:00:00Z" };
  assert.strictEqual(L.scheduleLine(s, NOW), "上次定时检查：10-14 07:00（北京时间），成功 14 / 失败 1；下次：明天 07:00");
});
test("scheduleLine: 尚未运行，下次是今天", () => {
  const s = { enabled: true, last_run_at: "0001-01-01T00:00:00Z", next_run_at: "2026-10-14T10:00:00Z" };
  assert.strictEqual(L.scheduleLine(s, NOW), "上次定时检查：尚未运行；下次：今天 18:00");
});
test("scheduleLine: 进行中", () => {
  const s = { enabled: true, last_run_at: "2026-10-14T03:00:00Z", total: 10, ok: 3, failed: 0, skipped: 0, next_run_at: "2026-10-14T23:00:00Z" };
  assert.strictEqual(L.scheduleLine(s, NOW), "定时检查进行中（3/10）；下次：明天 07:00");
});
test("scheduleLine: 下次更远显示日期，缺失则省略", () => {
  const s = { enabled: true, last_run_at: "2026-10-13T23:00:00Z", ok: 1, failed: 0, total: 1, next_run_at: "2026-10-16T23:00:00Z" };
  assert.ok(L.scheduleLine(s, NOW).endsWith("下次：10-17 07:00"));
  delete s.next_run_at;
  assert.ok(!L.scheduleLine(s, NOW).includes("下次"));
});
console.log(`live.test.js: ${passed} tests passed`);
