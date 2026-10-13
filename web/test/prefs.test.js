"use strict";
// 运行：node web/test/prefs.test.js
const assert = require("node:assert");
const P = require("../prefs.js");
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
const mem = (init = {}) => ({
  d: { ...init },
  getItem(k) {
    return k in this.d ? this.d[k] : null;
  },
  setItem(k, v) {
    this.d[k] = String(v);
  },
});
const throwing = {
  getItem() {
    throw new Error("denied");
  },
  setItem() {
    throw new Error("quota");
  },
};
const ALLOWED = [10, 20, 50];

test("loadPageSize: 合法值被读回", () => {
  assert.strictEqual(P.loadPageSize(mem({ k: "20" }), "k", ALLOWED, 10), 20);
  assert.strictEqual(P.loadPageSize(mem({ k: "50" }), "k", ALLOWED, 10), 50);
});
test("loadPageSize: 不在选项内 / 非法值用默认", () => {
  for (const v of ["15", "abc", "", null, "-10", "20.5", " ", "0", "1e1x"]) {
    const s = mem(v === null ? {} : { k: v });
    assert.strictEqual(P.loadPageSize(s, "k", ALLOWED, 10), 10, String(v));
  }
});
test("loadPageSize: getItem 抛错 / storage 为 null 用默认", () => {
  assert.strictEqual(P.loadPageSize(throwing, "k", ALLOWED, 10), 10);
  assert.strictEqual(P.loadPageSize(null, "k", ALLOWED, 10), 10);
});
test("savePageSize: 成功与失败", () => {
  const s = mem();
  assert.strictEqual(P.savePageSize(s, "k", 20), true);
  assert.strictEqual(s.d.k, "20");
  assert.strictEqual(P.savePageSize(throwing, "k", 20), false);
  assert.strictEqual(P.savePageSize(null, "k", 20), false);
});
test("optionValues: 读取 select 选项并过滤非法值", () => {
  const sel = { options: [{ value: "10" }, { value: "20" }, { value: "x" }, { value: "-5" }, { value: "50" }] };
  assert.deepStrictEqual(P.optionValues(sel), [10, 20, 50]);
  assert.deepStrictEqual(P.optionValues(null), []);
  assert.deepStrictEqual(P.optionValues({}), []);
});
test("safeStorage: getter 抛错返回 null", () => {
  const bad = {};
  Object.defineProperty(bad, "localStorage", {
    get() {
      throw new Error("SecurityError");
    },
  });
  assert.strictEqual(P.safeStorage(bad), null);
  const s = mem();
  assert.strictEqual(P.safeStorage({ localStorage: s }), s);
  assert.strictEqual(P.safeStorage({}), null);
});
console.log(`prefs.test.js: ${passed} tests passed`);
