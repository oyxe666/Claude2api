"use strict";
/*
 * 记住每页条数用到的纯函数，无依赖，可在 Node 里测试。
 * 所有 storage 操作都带 try/catch：隐私模式或禁用存储时只是不记住，不影响页面。
 */
(function (root) {
  // 读取 <select> 的全部选项值（正整数）。
  function optionValues(selectEl) {
    const out = [];
    const opts = (selectEl && selectEl.options) || [];
    for (let i = 0; i < opts.length; i++) {
      const n = parseInt(opts[i].value, 10);
      if (Number.isInteger(n) && n > 0) out.push(n);
    }
    return out;
  }

  // 读取已保存的每页条数；不在选项内、读取失败或没有 storage 时返回默认值。
  function loadPageSize(storage, key, allowed, def) {
    try {
      if (!storage) return def;
      const v = storage.getItem(key);
      if (v === null || v === undefined || String(v).trim() === "") return def;
      const n = Number(v);
      return Number.isInteger(n) && allowed.includes(n) ? n : def;
    } catch (e) {
      return def;
    }
  }

  // 保存每页条数；失败返回 false，不抛错。
  function savePageSize(storage, key, n) {
    try {
      if (!storage) return false;
      storage.setItem(key, String(n));
      return true;
    } catch (e) {
      return false;
    }
  }

  // 取 localStorage；访问它本身可能抛错（隐私模式 / 被禁用）。
  function safeStorage(r) {
    try {
      return r.localStorage || null;
    } catch (e) {
      return null;
    }
  }

  // 读取字符串选项；不在 allowed 内、读取失败或没有 storage 时返回默认值。
  function loadChoice(storage, key, allowed, def) {
    try {
      if (!storage) return def;
      const v = storage.getItem(key);
      if (typeof v !== "string") return def;
      return allowed.includes(v) ? v : def;
    } catch (e) {
      return def;
    }
  }

  // 保存字符串选项；失败返回 false，不抛错。
  function saveChoice(storage, key, value) {
    try {
      if (!storage) return false;
      storage.setItem(key, String(value));
      return true;
    } catch (e) {
      return false;
    }
  }

  const api = { optionValues, loadPageSize, savePageSize, safeStorage, loadChoice, saveChoice };
  root.PrefsUI = api;
  if (typeof module !== "undefined" && module.exports) module.exports = api;
})(typeof globalThis !== "undefined" ? globalThis : this);
