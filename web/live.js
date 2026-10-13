"use strict";
/*
 * 实时推送与检查账号用到的纯函数，无依赖，可在 Node 里测试。
 * 只做解析 / 计算 / 合并，不发任何请求。
 */
(function (root) {
  // 解析 SSE 文本缓冲：按空行切块，忽略以 ":" 开头的注释块（心跳），多行 data 用 "\n" 拼接。
  // 最后一块若还没遇到空行视为“半条”，原样放进 rest，等下一批数据补全。
  function parseSSE(buffer) {
    const parts = String(buffer).replace(/\r\n/g, "\n").split("\n\n");
    const rest = parts.pop();
    const events = [];
    for (const block of parts) {
      let event = "message";
      const data = [];
      let any = false;
      for (const line of block.split("\n")) {
        if (!line || line.startsWith(":")) continue;
        const i = line.indexOf(":");
        const field = i < 0 ? line : line.slice(0, i);
        let value = i < 0 ? "" : line.slice(i + 1);
        if (value.startsWith(" ")) value = value.slice(1);
        if (field === "event") {
          event = value;
          any = true;
        } else if (field === "data") {
          data.push(value);
          any = true;
        }
      }
      if (any) events.push({ event, data: data.join("\n") });
    }
    return { events, rest };
  }

  // 断线重连等待毫秒数：1 秒起指数退避，封顶 30 秒。
  function reconnectDelay(attempt) {
    const n = Math.max(0, Number(attempt) || 0);
    return Math.min(30000, 1000 * 2 ** n);
  }

  // 把推送来的账号浅合并进列表（按 email）。找到返回 true，找不到返回 false。
  // 浅合并会保留列表里原有的字段（例如推送里没有的 org_uuid）。
  function mergeEventAccount(list, acct) {
    if (!Array.isArray(list) || !acct || !acct.email) return false;
    const i = list.findIndex((x) => x.email === acct.email);
    if (i < 0) return false;
    Object.assign(list[i], acct);
    return true;
  }

  // 检查账号大约需要几分钟（至少 1 分钟）。
  function estimateMinutes(count, minSec, maxSec) {
    return Math.max(1, Math.ceil((count * (minSec + maxSec)) / 2 / 60));
  }

  const api = { parseSSE, reconnectDelay, mergeEventAccount, estimateMinutes };
  root.LiveUI = api;
  if (typeof module !== "undefined" && module.exports) module.exports = api;
})(typeof globalThis !== "undefined" ? globalThis : this);
