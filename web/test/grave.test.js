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
  return { createElement: mk, createTextNode: (t) => ({ tag: "#text", textContent: t, children: [], appendChild(c) { this.children.push(c); return c; } }) };
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


// ---- 曲线图 ----
test("chartPoints: 最大值为 0 时不除零，坐标有限", () => {
  const pts = G.chartPoints([{ date: "2026-10-13", count: 0 }, { date: "2026-10-14", count: 0 }], 640, 220, { l: 32, r: 12, t: 12, b: 28 });
  assert.strictEqual(pts.length, 2);
  for (const p of pts) assert.ok(Number.isFinite(p.x) && Number.isFinite(p.y));
});
test("chartPoints: 最大值在顶部，零在底部，x 递增", () => {
  const pad = { l: 32, r: 12, t: 12, b: 28 };
  const pts = G.chartPoints([{ date: "a", count: 0 }, { date: "b", count: 4 }], 640, 220, pad);
  assert.strictEqual(pts[1].y, pad.t);
  assert.strictEqual(pts[0].y, 220 - pad.b);
  assert.ok(pts[1].x > pts[0].x);
});
test("chartPoints: 只有一个点时居中", () => {
  const pts = G.chartPoints([{ date: "a", count: 2 }], 640, 220, { l: 32, r: 12, t: 12, b: 28 });
  assert.ok(Number.isFinite(pts[0].x));
});
test("buildChart: 全为 0 或空数据显示空状态", () => {
  const doc = makeDoc();
  for (const data of [[], null, [{ date: "a", count: 0 }]]) {
    const box = G.buildChart(doc, data);
    let text = "";
    walk(box, (n) => { if (n.textContent) text += n.textContent; });
    assert.ok(text.includes("这段时间没有账号阵亡"));
  }
});
test("buildChart: 有数据时生成网格、面积、折线和带提示的点", () => {
  const doc = svgDoc();
  const box = G.buildChart(doc, [{ date: "2026-10-13", count: 1 }, { date: "2026-10-14", count: 3 }]);
  const tags = [];
  const titles = [];
  const classes = [];
  walk(box, (n) => {
    tags.push(n.tag);
    if (n.tag === "title") titles.push(n.textContent);
    if (n.attrs && n.attrs.class) classes.push(n.tag + "." + n.attrs.class);
  });
  assert.ok(tags.includes("polyline"));
  assert.strictEqual(tags.filter((t) => t === "circle").length, 2);
  assert.ok(titles.includes("2026-10-14：3 个"));
  assert.ok(classes.includes("line.chart-grid"));
  assert.ok(classes.includes("polygon.chart-area"));
});
// ---- 存活统计 ----
function svgDoc() {
  const d = makeDoc();
  d.createElementNS = (ns, tag) => {
    const e = d.createElement(tag);
    e.attrs = {};
    e.setAttribute = (k, v) => { e.attrs[k] = v; };
    return e;
  };
  return d;
}
test("buildSurvivalSummary: 正常与分母为 0", () => {
  const t = G.buildSurvivalSummary(doc, { imported: 4, alive_rate: 0.5, eligible_7: 3, survived_7: 2, rate_7: 2 / 3, eligible_30: 2, survived_30: 1, rate_30: 0.5 }).map(allText).join("#");
  for (const s of ["累计导入|4", "当前存活率|50%", "活过 7 天|67%（2/3）", "活过 30 天|50%（1/2）"]) assert.ok(t.includes(s), s);
  const e = G.buildSurvivalSummary(doc, {}).map(allText).join("#");
  assert.ok(e.includes("当前存活率|—") && e.includes("活过 7 天|—") && e.includes("活过 30 天|—"));
  assert.strictEqual(G.buildSurvivalSummary(doc, null).length, 4);
});
test("curvePoints: 1 在顶部，0 在底部，单点居中，坐标有限", () => {
  const pad = { l: 40, r: 12, t: 12, b: 28 };
  const pts = G.curvePoints([{ day: 0, survival: 1 }, { day: 1, survival: 0 }], 640, 220, pad);
  assert.strictEqual(pts[0].y, pad.t);
  assert.strictEqual(pts[1].y, 220 - pad.b);
  assert.ok(pts[1].x > pts[0].x);
  const one = G.curvePoints([{ day: 0, survival: 1 }], 640, 220, pad);
  assert.ok(Number.isFinite(one[0].x));
  const bad = G.curvePoints([{ day: 0, survival: NaN }], 640, 220, pad);
  assert.ok(Number.isFinite(bad[0].y));
});
test("buildSurvivalCurve: 空数据显示空状态；有数据生成网格、面积、折线和提示", () => {
  for (const data of [[], null]) {
    const box = G.buildSurvivalCurve(makeDoc(), data);
    assert.ok(allText(box).includes("还没有足够的数据"));
  }
  const box = G.buildSurvivalCurve(svgDoc(), [{ day: 0, survival: 1 }, { day: 1, survival: 0.5 }]);
  const tags = [];
  const titles = [];
  const classes = [];
  walk(box, (n) => {
    tags.push(n.tag);
    if (n.tag === "title") titles.push(n.textContent);
    if (n.attrs && n.attrs.class) classes.push(n.tag + "." + n.attrs.class);
  });
  assert.ok(tags.includes("polyline"));
  assert.ok(titles.includes("第 1 天：50% 存活"));
  assert.ok(classes.includes("line.chart-grid"));
  assert.ok(classes.includes("polygon.chart-area"));
});
test("dualPoints: 最大值为 0 时不除零；共用同一纵轴", () => {
  const pad = { l: 32, r: 12, t: 12, b: 28 };
  const z = G.dualPoints([{ date: "a", imported: 0, died: 0 }, { date: "b", imported: 0, died: 0 }], 640, 220, pad);
  for (const p of z.imported.concat(z.died)) assert.ok(Number.isFinite(p.x) && Number.isFinite(p.y));
  const d = G.dualPoints([{ date: "a", imported: 4, died: 1 }, { date: "b", imported: 0, died: 4 }], 640, 220, pad);
  assert.strictEqual(d.max, 4);
  assert.strictEqual(d.imported[0].y, pad.t);
  assert.strictEqual(d.died[1].y, pad.t);
});
test("buildDualChart: 空数据空状态；有数据两条线、圆点与方块、图例", () => {
  for (const data of [[], null, [{ date: "a", imported: 0, died: 0 }]]) {
    assert.ok(allText(G.buildDualChart(makeDoc(), data)).includes("这段时间没有导入或阵亡"));
  }
  const box = G.buildDualChart(svgDoc(), [{ date: "2026-10-13", imported: 2, died: 0 }, { date: "2026-10-14", imported: 1, died: 3 }]);
  const tags = [];
  const titles = [];
  const classes = [];
  walk(box, (n) => {
    tags.push(n.tag);
    if (n.tag === "title") titles.push(n.textContent);
    if (n.attrs && n.attrs.class) classes.push(n.tag + "." + n.attrs.class);
    if (n.className) classes.push("div." + n.className);
  });
  assert.strictEqual(tags.filter((t) => t === "polyline").length, 2);
  assert.strictEqual(tags.filter((t) => t === "circle").length, 2);
  assert.strictEqual(tags.filter((t) => t === "rect").length, 2);
  assert.ok(titles.includes("2026-10-14：阵亡 3 个"));
  assert.ok(titles.includes("2026-10-13：导入 2 个"));
  assert.ok(classes.includes("line.chart-grid"));
  assert.ok(classes.includes("polygon.chart-area-imported") || classes.includes("polygon.chart-area chart-area-imported"));
  assert.ok(classes.includes("div.chart-legend"));
});
console.log(`grave.test.js: ${passed} tests passed`);
