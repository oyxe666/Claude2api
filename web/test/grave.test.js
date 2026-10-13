"use strict";
// 运行：node web/test/grave.test.js
const assert = require("node:assert");
const G = require("../grave.js");
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
function makeDoc() {
  const mk = (tag) => ({
    tag,
    className: "",
    textContent: "",
    children: [],
    appendChild(c) {
      this.children.push(c);
      return c;
    },
  });
  return { createElement: mk };
}
function walk(n, fn) {
  fn(n);
  for (const c of n.children) walk(c, fn);
}
const allText = (n) => {
  const t = [];
  walk(n, (x) => x.textContent && t.push(x.textContent));
  return t.join("|");
};
const doc = makeDoc();

test("reasonLabel", () => {
  assert.strictEqual(G.reasonLabel("session_invalid"), "会话失效");
  assert.strictEqual(G.reasonLabel("auto_removed"), "自动清理");
  assert.strictEqual(G.reasonLabel("manual_delete"), "手动删除");
  assert.strictEqual(G.reasonLabel("zzz"), "未知原因");
  assert.strictEqual(G.reasonLabel(undefined), "未知原因");
});
test("fmtLifespan 边界", () => {
  assert.strictEqual(G.fmtLifespan(0), "不到 1 分钟");
  assert.strictEqual(G.fmtLifespan(59), "不到 1 分钟");
  assert.strictEqual(G.fmtLifespan(60), "1 分钟");
  assert.strictEqual(G.fmtLifespan(3599), "59 分钟");
  assert.strictEqual(G.fmtLifespan(3600), "1 小时");
  assert.strictEqual(G.fmtLifespan(86399), "23 小时");
  assert.strictEqual(G.fmtLifespan(86400), "1 天");
  assert.strictEqual(G.fmtLifespan(86400 + 4 * 3600), "1 天 4 小时");
  assert.strictEqual(G.fmtLifespan(-1), "—");
  assert.strictEqual(G.fmtLifespan(NaN), "—");
  assert.strictEqual(G.fmtLifespan(undefined), "—");
});
test("quotaText", () => {
  assert.strictEqual(G.quotaText({ five_hour: { utilization: 0.42 }, seven_day: { utilization: 0.1 } }), "5 小时用了 42% · 7 天用了 10%");
  assert.strictEqual(G.quotaText({ five_hour: { utilization: 0.5 } }), "5 小时用了 50% · 7 天用了 —");
  assert.strictEqual(G.quotaText(null), "无额度记录");
  assert.strictEqual(G.quotaText({}), "无额度记录");
  assert.strictEqual(G.quotaText({ updated_at: "2026-01-01T00:00:00Z" }), "无额度记录");
});
test("epitaph", () => {
  assert.ok(G.epitaph({ reason: "session_invalid", lifespan_sec: 7200, chat_count: 5 }).includes("2 小时"));
  assert.ok(G.epitaph({ reason: "session_invalid", lifespan_sec: 7200, chat_count: 5 }).includes("5 次"));
  assert.ok(G.epitaph({ reason: "manual_delete", lifespan_sec: 10, chat_count: 0 }).includes("还没来得及干活"));
  assert.ok(G.epitaph({ reason: "auto_removed", lifespan_sec: 10, chat_count: 1 }).includes("自动清理"));
});
test("buildTomb: 文本含邮箱/原因/对话次数/额度", () => {
  const card = G.buildTomb(doc, {
    email: "a@example.com",
    reason: "auto_removed",
    chat_count: 7,
    lifespan_sec: 90000,
    died_at: "2026-01-01T00:00:00Z",
    last_quota: { five_hour: { utilization: 0.9 } },
  }, { fmtTime: () => "T0" });
  assert.strictEqual(card.className, "grave-card");
  const t = allText(card);
  for (const s of ["a@example.com", "自动清理", "7 次", "1 天 1 小时", "T0", "5 小时用了 90%"]) assert.ok(t.includes(s), s);
});
test("buildTomb: 邮箱含 <script> 原样作为 textContent", () => {
  const evil = "<script>alert(1)</script>@x.com";
  const card = G.buildTomb(doc, { email: evil, reason: "manual_delete", chat_count: 0, lifespan_sec: 5 });
  let found = false;
  walk(card, (n) => {
    if (n.textContent === evil) found = true;
    assert.notStrictEqual(n.tag, "script");
  });
  assert.ok(found);
});
test("buildEmpty", () => {
  assert.strictEqual(G.buildEmpty(doc).textContent, "还没有账号阵亡");
});
test("buildStats", () => {
  const spans = G.buildStats(doc, { total: 3, avg_lifespan_sec: 7200, last_7_days: 2 });
  assert.strictEqual(spans.length, 3);
  const t = spans.map(allText).join("#");
  for (const s of ["累计阵亡|3", "平均寿命|2 小时", "近 7 天阵亡|2"]) assert.ok(t.includes(s), s);
  const empty = G.buildStats(doc, { total: 0 }).map(allText).join("#");
  assert.ok(empty.includes("平均寿命|—"));
});
console.log(`grave.test.js: ${passed} tests passed`);
