"use strict";
// 运行：node web/test/quota.test.js
const assert = require("node:assert");
const Q = require("../quota.js");

const NOW = Date.UTC(2025, 4, 12, 10, 0, 0); // 固定“现在”
const sec = (ms) => Math.floor(ms / 1000);
const MIN = 60000;
const HOUR = 3600000;
const DAY = 86400000;
const iso = (ms) => new Date(ms).toISOString();

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

// ---- 最小 document stub ----
function makeDoc() {
  const mk = (tag) => ({
    tag,
    className: "",
    textContent: "",
    attrs: {},
    style: {},
    children: [],
    disabled: false,
    setAttribute(k, v) {
      this.attrs[k] = String(v);
    },
    getAttribute(k) {
      return this.attrs[k];
    },
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
function find(n, pred) {
  const r = [];
  walk(n, (x) => pred(x) && r.push(x));
  return r;
}
const hasCls = (n, c) => n.className.split(/\s+/).includes(c);
const allText = (n) => {
  const t = [];
  walk(n, (x) => x.textContent && t.push(x.textContent));
  return t.join("|");
};

const win = (util, resetsMs, status) => ({
  utilization: util,
  resets_at: resetsMs == null ? 0 : sec(resetsMs),
  status: status || "",
});
const acct = (email, five, seven, extra = {}) => ({
  email,
  status: "active",
  has_session: true,
  quota: { updated_at: iso(NOW - 5 * MIN), five_hour: five, seven_day: seven },
  ...extra,
});

// ---- windowState ----
test("windowState: 空 / 无数据", () => {
  assert.strictEqual(Q.windowState(null, NOW).state, "nodata");
  assert.strictEqual(Q.windowState(undefined, NOW).state, "nodata");
  assert.strictEqual(Q.windowState({}, NOW).state, "nodata");
  assert.strictEqual(Q.windowState({ resets_at: sec(NOW + HOUR) }, NOW).state, "nodata");
  assert.strictEqual(Q.windowState({}, NOW).remaining, null);
});
test("windowState: ok 与边界 util=0", () => {
  const s = Q.windowState(win(0, NOW + HOUR), NOW);
  assert.strictEqual(s.state, "ok");
  assert.strictEqual(s.remaining, 100);
  assert.strictEqual(s.resetsAtMs, sec(NOW + HOUR) * 1000);
});
test("windowState: util=1 耗尽，>1 夹到 1，负数夹到 0", () => {
  assert.strictEqual(Q.windowState(win(1, NOW + HOUR), NOW).state, "exhausted");
  assert.strictEqual(Q.windowState(win(1, NOW + HOUR), NOW).remaining, 0);
  assert.strictEqual(Q.windowState(win(1.7, NOW + HOUR), NOW).remaining, 0);
  assert.strictEqual(Q.windowState(win(-0.3, NOW + HOUR), NOW).remaining, 100);
});
test("windowState: status 含 exceeded 即耗尽", () => {
  const s = Q.windowState(win(0.5, NOW + HOUR, "exceeded_limit"), NOW);
  assert.strictEqual(s.state, "exhausted");
  const s2 = Q.windowState({ status: "exceeded_limit", resets_at: sec(NOW + HOUR) }, NOW);
  assert.strictEqual(s2.state, "exhausted");
});
test("windowState: 已重置按 100%（即使旧用量=1）", () => {
  const s = Q.windowState(win(1, NOW - MIN, "exceeded_limit"), NOW);
  assert.strictEqual(s.state, "reset");
  assert.strictEqual(s.remaining, 100);
  assert.strictEqual(Q.windowState({ resets_at: sec(NOW - HOUR) }, NOW).state, "reset");
});
test("windowState: 浮点噪声被消除", () => {
  assert.strictEqual(Q.windowState(win(0.07, NOW + HOUR), NOW).remaining, 93);
  assert.strictEqual(Q.windowState(win(0.8, NOW + HOUR), NOW).remaining, 20);
  assert.strictEqual(Q.windowState(win(0.41, NOW + HOUR), NOW).remaining, 59);
});

// ---- colorLevel ----
test("colorLevel 边界", () => {
  assert.strictEqual(Q.colorLevel(100), "high");
  assert.strictEqual(Q.colorLevel(50.1), "high");
  assert.strictEqual(Q.colorLevel(50), "mid");
  assert.strictEqual(Q.colorLevel(20), "mid");
  assert.strictEqual(Q.colorLevel(19.9), "low");
  assert.strictEqual(Q.colorLevel(0), "low");
  assert.strictEqual(Q.colorLevel(null), "none");
  assert.strictEqual(Q.colorLevel(undefined), "none");
});

// ---- fmtCountdown / fmtAgo / fmtLocalTime ----
test("fmtCountdown", () => {
  assert.strictEqual(Q.fmtCountdown(0), "不到 1 分钟");
  assert.strictEqual(Q.fmtCountdown(-5), "不到 1 分钟");
  assert.strictEqual(Q.fmtCountdown(59999), "不到 1 分钟");
  assert.strictEqual(Q.fmtCountdown(MIN), "1 分钟");
  assert.strictEqual(Q.fmtCountdown(59 * MIN + 59000), "59 分钟");
  assert.strictEqual(Q.fmtCountdown(HOUR), "1 小时");
  assert.strictEqual(Q.fmtCountdown(2 * HOUR + 10 * MIN), "2 小时 10 分");
  assert.strictEqual(Q.fmtCountdown(DAY), "1 天");
  assert.strictEqual(Q.fmtCountdown(DAY + 3 * HOUR + 5 * MIN), "1 天 3 小时");
  assert.strictEqual(Q.fmtCountdown(6 * DAY + 23 * HOUR), "6 天 23 小时");
});
test("fmtAgo", () => {
  assert.strictEqual(Q.fmtAgo(NOW, NOW), "刚刚");
  assert.strictEqual(Q.fmtAgo(NOW - 59999, NOW), "刚刚");
  assert.strictEqual(Q.fmtAgo(NOW - MIN, NOW), "1 分钟前");
  assert.strictEqual(Q.fmtAgo(NOW - 59 * MIN, NOW), "59 分钟前");
  assert.strictEqual(Q.fmtAgo(NOW - HOUR, NOW), "1 小时前");
  assert.strictEqual(Q.fmtAgo(NOW - 23 * HOUR, NOW), "23 小时前");
  assert.strictEqual(Q.fmtAgo(NOW - DAY, NOW), "1 天前");
  assert.strictEqual(Q.fmtAgo(null, NOW), "暂无");
  assert.strictEqual(Q.fmtAgo(NOW + 5000, NOW), "刚刚"); // 时钟略超前
});
test("fmtLocalTime 本地时区 MM-DD HH:mm", () => {
  const ms = new Date(2025, 0, 5, 9, 7).getTime();
  assert.strictEqual(Q.fmtLocalTime(ms), "01-05 09:07");
});

// ---- accountView ----
test("accountView: usable / hasData / exhausted / recoverAt 取最大", () => {
  const r5 = NOW + 2 * HOUR;
  const r7 = NOW + 3 * DAY;
  const v = Q.accountView(acct("a@x", win(1, r5), win(1, r7)), NOW);
  assert.strictEqual(v.usable, true);
  assert.strictEqual(v.hasData, true);
  assert.strictEqual(v.exhausted, true);
  assert.strictEqual(v.recoverAtMs, sec(r7) * 1000);
  const only5 = Q.accountView(acct("a@x", win(1, r5), win(0.2, r7)), NOW);
  assert.strictEqual(only5.recoverAtMs, sec(r5) * 1000);
});
test("accountView: 耗尽窗口无重置时间 → recoverAt 未知", () => {
  const v = Q.accountView(acct("a@x", { status: "exceeded_limit" }, win(1, NOW + HOUR)), NOW);
  assert.strictEqual(v.exhausted, true);
  assert.strictEqual(v.recoverAtMs, null);
});
test("accountView: expired / has_session=false 不可用，不算 exhausted", () => {
  const e = Q.accountView(acct("a@x", win(1, NOW + HOUR), null, { status: "expired" }), NOW);
  assert.strictEqual(e.usable, false);
  assert.strictEqual(e.exhausted, false);
  const n = Q.accountView(acct("a@x", win(0.1, NOW + HOUR), null, { has_session: false }), NOW);
  assert.strictEqual(n.usable, false);
});
test("accountView: 无 quota", () => {
  const v = Q.accountView({ email: "a@x", status: "active" }, NOW);
  assert.strictEqual(v.hasData, false);
  assert.strictEqual(v.failed, false);
  assert.strictEqual(v.updatedAtMs, null);
});
test("accountView.failed: error_at 与 updated_at 先后", () => {
  const mk = (updated, errAt, err = "请求过于频繁（429），请稍后再试") => ({
    email: "a@x",
    status: "active",
    quota: { updated_at: updated, error_at: errAt, error: err, five_hour: win(0.3, NOW + HOUR) },
  });
  const later = Q.accountView(mk(iso(NOW - HOUR), iso(NOW - MIN)), NOW);
  assert.strictEqual(later.failed, true);
  assert.strictEqual(later.failMsg, "请求过于频繁（429），请稍后再试");
  assert.strictEqual(Q.accountView(mk(iso(NOW - MIN), iso(NOW - HOUR)), NOW).failed, false); // 之后又成功
  assert.strictEqual(Q.accountView(mk(iso(NOW), iso(NOW)), NOW).failed, false); // 相等不算
  assert.strictEqual(Q.accountView(mk(iso(NOW), iso(NOW + MIN), ""), NOW).failed, false); // 无 error 文本
  // 从未成功过：updated_at 为 Go 零值 → 视为无
  const never = Q.accountView(mk("0001-01-01T00:00:00Z", iso(NOW - MIN)), NOW);
  assert.strictEqual(never.failed, true);
  assert.strictEqual(never.updatedAtMs, null);
  assert.strictEqual(Q.accountView(mk("", iso(NOW - MIN)), NOW).failed, true);
  // error_at 缺失或为零值 → 不算失败
  assert.strictEqual(Q.accountView(mk(iso(NOW), "0001-01-01T00:00:00Z"), NOW).failed, false);
  assert.strictEqual(Q.accountView(mk(iso(NOW), undefined), NOW).failed, false);
});

// ---- summarize ----
test("summarize: 无账号 / 无数据", () => {
  const e = Q.summarize([], NOW);
  assert.strictEqual(e.total, 0);
  assert.strictEqual(e.hasAnyData, false);
  assert.strictEqual(e.avg5, null);
  const n = Q.summarize([{ email: "a", status: "active" }, { email: "b", status: "active" }], NOW);
  assert.strictEqual(n.total, 2);
  assert.strictEqual(n.available, 2); // 无数据的可用账号算可用
  assert.strictEqual(n.hasAnyData, false);
  assert.strictEqual(n.avg5, null);
  assert.strictEqual(n.nextRecovery, null);
  assert.strictEqual(n.recoveryUnknown, false);
});
test("summarize: 全可用，等权平均", () => {
  const s = Q.summarize(
    [
      acct("a", win(0.2, NOW + HOUR), win(0.4, NOW + DAY)),
      acct("b", win(0.6, NOW + HOUR), win(0.6, NOW + DAY)),
    ],
    NOW,
  );
  assert.strictEqual(s.available, 2);
  assert.strictEqual(s.unavailable, 0);
  assert.strictEqual(s.avg5, 60); // (80+40)/2
  assert.strictEqual(s.avg7, 50); // (60+40)/2
  assert.strictEqual(s.n5, 2);
  assert.strictEqual(s.nextRecovery, null);
  assert.strictEqual(s.recoveryUnknown, false);
  assert.strictEqual(s.hasAnyData, true);
});
test("summarize: 部分耗尽（耗尽按 0 计入平均），恢复时间取最小", () => {
  const s = Q.summarize(
    [
      acct("a", win(1, NOW + 2 * HOUR), win(0.5, NOW + DAY)),
      acct("b", win(0, NOW + HOUR), win(0, NOW + DAY)),
    ],
    NOW,
  );
  assert.strictEqual(s.total, 2);
  assert.strictEqual(s.available, 1);
  assert.strictEqual(s.unavailable, 1);
  assert.strictEqual(s.avg5, 50); // (0+100)/2
  assert.strictEqual(s.avg7, 75);
  assert.strictEqual(s.nextRecovery.atMs, sec(NOW + 2 * HOUR) * 1000);
  assert.strictEqual(s.nextRecovery.count, 1);
});
test("summarize: expired 账号不入平均也不算可用", () => {
  const s = Q.summarize(
    [
      acct("a", win(0.5, NOW + HOUR), win(0.5, NOW + DAY)),
      acct("dead", win(0, NOW + HOUR), win(0, NOW + DAY), { status: "expired" }),
    ],
    NOW,
  );
  assert.strictEqual(s.total, 2);
  assert.strictEqual(s.available, 1);
  assert.strictEqual(s.avg5, 50);
  assert.strictEqual(s.n5, 1);
});
test("summarize: 只有 expired 账号带数据 → hasAnyData=false", () => {
  const s = Q.summarize([acct("dead", win(0, NOW + HOUR), null, { status: "expired" })], NOW);
  assert.strictEqual(s.hasAnyData, false);
  assert.strictEqual(s.available, 0);
});
test("summarize: 恢复时间相近合并 count（60 秒内）", () => {
  const t = NOW + HOUR;
  const s = Q.summarize(
    [
      acct("a", win(1, t), null),
      acct("b", win(1, t + 30 * 1000), null),
      acct("c", win(1, t + 5 * MIN), null),
    ],
    NOW,
  );
  assert.strictEqual(s.unavailable, 3);
  assert.strictEqual(s.nextRecovery.atMs, sec(t) * 1000);
  assert.strictEqual(s.nextRecovery.count, 2);
});
test("summarize: 耗尽但恢复时间都未知", () => {
  const s = Q.summarize([acct("a", { status: "exceeded_limit" }, null)], NOW);
  assert.strictEqual(s.unavailable, 1);
  assert.strictEqual(s.nextRecovery, null);
  assert.strictEqual(s.recoveryUnknown, true);
});
test("summarize: 部分耗尽账号有恢复时间、部分没有 → 取已知中最小，不标未知", () => {
  const s = Q.summarize(
    [acct("a", { status: "exceeded_limit" }, null), acct("b", win(1, NOW + HOUR), null)],
    NOW,
  );
  assert.strictEqual(s.recoveryUnknown, false);
  assert.strictEqual(s.nextRecovery.count, 1);
});
test("summarize: 过期的耗尽窗口按重置后重新计入可用", () => {
  const s = Q.summarize([acct("a", win(1, NOW - MIN, "exceeded_limit"), win(1, NOW - HOUR))], NOW);
  assert.strictEqual(s.available, 1);
  assert.strictEqual(s.unavailable, 0);
  assert.strictEqual(s.avg5, 100);
  assert.strictEqual(s.avg7, 100);
  assert.strictEqual(s.nextRecovery, null);
});
test("summarize: 只有一个窗口有数据时另一窗口平均为 null", () => {
  const s = Q.summarize([acct("a", win(0.3, NOW + HOUR), null)], NOW);
  assert.strictEqual(s.avg5, 70);
  assert.strictEqual(s.avg7, null);
  assert.strictEqual(s.n7, 0);
});

// ---- overviewText ----
test("overviewText 文案", () => {
  const none = Q.overviewText(Q.summarize([{ email: "a", status: "active" }], NOW), NOW);
  assert.strictEqual(none.empty, "暂无数据，点击「检查账号」");
  assert.strictEqual(none.available, "1/1");
  assert.strictEqual(none.avg5, "—");
  assert.strictEqual(none.recovery, "—");

  const ok = Q.overviewText(Q.summarize([acct("a", win(0.375, NOW + HOUR), win(0.5, NOW + DAY))], NOW), NOW);
  assert.strictEqual(ok.empty, "");
  assert.strictEqual(ok.avg5, "63%");
  assert.strictEqual(ok.avg7, "50%");
  assert.strictEqual(ok.recovery, "全部可用");
  assert.strictEqual(ok.note, "按账号等权平均，仅供参考");

  const part = Q.overviewText(
    Q.summarize([acct("a", win(1, NOW + 2 * HOUR + 10 * MIN), null), acct("b", win(0.1, NOW + HOUR), null)], NOW),
    NOW,
  );
  assert.strictEqual(part.recovery, "2 小时 10 分后恢复 1 个号");
  assert.strictEqual(part.available, "1/2");

  const unk = Q.overviewText(Q.summarize([acct("a", { status: "exceeded_limit" }, null)], NOW), NOW);
  assert.strictEqual(unk.recovery, "恢复时间未知");

  const soon = Q.overviewText(Q.summarize([acct("a", win(1, NOW + 20 * 1000), null)], NOW), NOW);
  assert.strictEqual(soon.recovery, "即将恢复 1 个号");
});

// ---- DOM 构建 ----
const EVIL = "<img src=x onerror=alert(1)>@evil.com";
test("buildCard: 恶意邮箱只进 textContent / attribute，无 innerHTML", () => {
  const doc = makeDoc();
  const card = Q.buildCard(doc, acct(EVIL, win(0.3, NOW + HOUR), win(0.9, NOW + DAY)), NOW, {});
  assert.strictEqual(card.innerHTML, undefined);
  const emails = find(card, (n) => hasCls(n, "quota-email"));
  assert.strictEqual(emails.length, 1);
  assert.strictEqual(emails[0].textContent, EVIL);
  assert.strictEqual(card.getAttribute("data-email"), EVIL);
  const rb = find(card, (n) => n.getAttribute("data-act") === "refresh-quota")[0];
  assert.strictEqual(rb.getAttribute("aria-label"), `刷新 ${EVIL} 的额度`);
  // 没有任何元素的 tag 是 img/script
  assert.strictEqual(find(card, (n) => n.tag === "img" || n.tag === "script").length, 0);
});
test("buildCard: 恶意错误文案只进 textContent", () => {
  const doc = makeDoc();
  const a = acct("a@x", win(0.3, NOW + HOUR), null);
  a.quota.error = "<script>alert(1)</script>";
  a.quota.error_at = iso(NOW - MIN);
  const card = Q.buildCard(doc, a, NOW, {});
  assert.strictEqual(find(card, (n) => n.tag === "script").length, 0);
  assert.ok(allText(card).includes("<script>alert(1)</script>"));
});
test("buildCard: 进度条 role / aria / 颜色类 / 宽度", () => {
  const doc = makeDoc();
  const card = Q.buildCard(doc, acct("a@x", win(0.5, NOW + HOUR), win(0.85, NOW + DAY)), NOW, {});
  const bars = find(card, (n) => n.getAttribute("role") === "progressbar");
  assert.strictEqual(bars.length, 2);
  assert.strictEqual(bars[0].getAttribute("aria-valuemin"), "0");
  assert.strictEqual(bars[0].getAttribute("aria-valuemax"), "100");
  assert.strictEqual(bars[0].getAttribute("aria-valuenow"), "50");
  assert.strictEqual(bars[0].getAttribute("aria-label"), "5 小时剩余额度");
  assert.strictEqual(bars[1].getAttribute("aria-valuenow"), "15");
  assert.strictEqual(bars[1].getAttribute("aria-label"), "7 天剩余额度");
  assert.ok(hasCls(bars[0].children[0], "bar-mid"));
  assert.ok(hasCls(bars[1].children[0], "bar-low"));
  assert.strictEqual(bars[0].children[0].style.width, "50%");
  const t = allText(card);
  assert.ok(t.includes("剩余 50%") && t.includes("剩余 15%"));
  assert.ok(t.includes("1 小时后重置 · "));
  assert.ok(t.includes("更新于 5 分钟前"));
});
test("buildCard: 绿色", () => {
  const card = Q.buildCard(makeDoc(), acct("a@x", win(0.1, NOW + HOUR), win(0.1, NOW + HOUR)), NOW, {});
  const fills = find(card, (n) => hasCls(n, "quota-fill"));
  assert.ok(fills.every((f) => hasCls(f, "bar-high")));
});
test("buildCard: 耗尽灰卡 + 恢复徽标", () => {
  const card = Q.buildCard(makeDoc(), acct("a@x", win(1, NOW + 45 * MIN), win(0.2, NOW + DAY)), NOW, {});
  assert.ok(hasCls(card, "is-exhausted"));
  assert.ok(!hasCls(card, "is-failed"));
  const b = find(card, (n) => hasCls(n, "b-exhausted"));
  assert.strictEqual(b[0].textContent, "已耗尽 · 45 分钟后恢复");
  const unk = Q.buildCard(makeDoc(), acct("a@x", { status: "exceeded_limit" }, null), NOW, {});
  assert.strictEqual(find(unk, (n) => hasCls(n, "b-exhausted"))[0].textContent, "已耗尽");
});
test("buildCard: 已重置 / 暂无", () => {
  const card = Q.buildCard(makeDoc(), acct("a@x", win(1, NOW - MIN), null), NOW, {});
  const t = allText(card);
  assert.ok(t.includes("已重置"));
  assert.ok(t.includes("剩余 100%"));
  assert.ok(t.includes("暂无"));
  const bars = find(card, (n) => n.getAttribute("role") === "progressbar");
  assert.strictEqual(bars[0].getAttribute("aria-valuenow"), "100");
  assert.strictEqual(bars[1].getAttribute("aria-valuenow"), undefined);
  assert.strictEqual(bars[1].getAttribute("aria-valuetext"), "暂无数据");
  assert.ok(!hasCls(card, "is-exhausted"));
});
test("buildCard: 失败卡带原因，保留旧数据，不变灰", () => {
  const a = acct("a@x", win(0.3, NOW + HOUR), win(0.3, NOW + DAY));
  a.quota.error = "请求过于频繁（429），请稍后再试";
  a.quota.error_at = iso(NOW - MIN);
  const card = Q.buildCard(makeDoc(), a, NOW, {});
  assert.ok(hasCls(card, "is-failed"));
  assert.ok(!hasCls(card, "is-exhausted"));
  assert.strictEqual(find(card, (n) => hasCls(n, "b-failed"))[0].textContent, "失败");
  assert.ok(find(card, (n) => hasCls(n, "quota-fail"))[0].textContent.includes("请求过于频繁（429），请稍后再试"));
  assert.ok(allText(card).includes("剩余 70%"));
});
test("buildCard: 失效账号", () => {
  const card = Q.buildCard(makeDoc(), acct("a@x", win(1, NOW + HOUR), null, { status: "expired" }), NOW, {});
  assert.ok(hasCls(card, "is-expired"));
  assert.ok(!hasCls(card, "is-exhausted"));
});
test("buildCard: 刷新按钮 aria-label / loading / 无时间显示暂无", () => {
  const a = { email: "a@x", status: "active" };
  const idle = Q.buildCard(makeDoc(), a, NOW, { refreshing: () => false });
  const r1 = find(idle, (n) => n.getAttribute("data-act") === "refresh-quota")[0];
  assert.strictEqual(r1.getAttribute("aria-label"), "刷新 a@x 的额度");
  assert.strictEqual(r1.disabled, false);
  assert.ok(!hasCls(r1, "is-loading"));
  assert.ok(allText(idle).includes("更新于 暂无"));
  const busy = Q.buildCard(makeDoc(), a, NOW, { refreshing: (e) => e === "a@x" });
  const r2 = find(busy, (n) => n.getAttribute("data-act") === "refresh-quota")[0];
  assert.ok(hasCls(r2, "is-loading"));
  assert.strictEqual(r2.disabled, true);
  assert.strictEqual(r2.getAttribute("aria-busy"), "true");
  // 账号操作按钮齐全
  const acts = find(busy, (n) => n.tag === "button").map((b) => b.getAttribute("data-act"));
  assert.deepStrictEqual(acts, ["refresh-quota", "refresh-acct", "detail", "delete"]);
});
test("buildCard: 单账号刷新按钮 title 写明会向 Claude 发 1 次请求", () => {
  const card = Q.buildCard(makeDoc(), { email: "a@x", status: "active" }, NOW, {});
  for (const act of ["refresh-quota", "refresh-acct"]) {
    const b = find(card, (n) => n.getAttribute("data-act") === act)[0];
    assert.strictEqual(b.getAttribute("title"), "会向 Claude 发 1 次请求", act);
  }
});
test("buildOverview: 有数据", () => {
  const doc = makeDoc();
  const s = Q.summarize(
    [acct("a", win(1, NOW + 2 * HOUR + 10 * MIN), win(0.5, NOW + DAY)), acct("b", win(0.2, NOW + HOUR), win(0.2, NOW + DAY))],
    NOW,
  );
  const box = Q.buildOverview(doc, s, { nowMs: NOW });
  const t = allText(box);
  for (const k of ["可用账号", "1/2", "5 小时剩余", "40%", "7 天剩余", "65%", "最近恢复", "2 小时 10 分后恢复 1 个号", "按账号等权平均，仅供参考"]) {
    assert.ok(t.includes(k), "缺少: " + k);
  }
  const bars = find(box, (n) => n.getAttribute("role") === "progressbar");
  assert.strictEqual(bars.length, 2);
  assert.strictEqual(bars[0].getAttribute("aria-valuenow"), "40");
  assert.ok(!t.includes("刷新全部额度"));
  assert.strictEqual(find(box, (n) => n.getAttribute("data-act") === "refresh-all-quota").length, 0);
});
test("buildOverview: 无数据显示提示；批次中显示 n/N（不再有总览刷新按钮）", () => {
  const s = Q.summarize([{ email: "a", status: "active" }], NOW);
  const idle = Q.buildOverview(makeDoc(), s, { nowMs: NOW });
  assert.ok(allText(idle).includes("暂无数据，点击「检查账号」"));
  assert.ok(!allText(idle).includes("按账号等权平均"));
  const run = Q.buildOverview(makeDoc(), s, { nowMs: NOW, batch: { running: true, done: 2, total: 5 } });
  assert.strictEqual(find(run, (n) => n.tag === "button").length, 0);
  assert.ok(find(run, (n) => hasCls(n, "quota-progress"))[0].textContent === "2/5");
});

console.log(`quota.test.js: ${passed} tests passed`);
