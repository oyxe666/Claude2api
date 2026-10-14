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

  // 预期 5 小时窗口时间：定时时间往后推 5 小时。非法输入返回 null。
  // 返回 { start: "HH:MM", end: "HH:MM", nextDay: bool }，nextDay 表示结束时间跨午夜到了次日。
  function windowEstimate(hhmm) {
    const m = /^(\d{1,2}):(\d{2})$/.exec(String(hhmm || "").trim());
    if (!m) return null;
    const h = Number(m[1]), mi = Number(m[2]);
    if (h > 23 || mi > 59) return null;
    const total = h * 60 + mi + 5 * 60;
    const eh = Math.floor((total % 1440) / 60), em = total % 60;
    const pad = (n) => String(n).padStart(2, "0");
    return { start: pad(h) + ":" + pad(mi), end: pad(eh) + ":" + pad(em), nextDay: total >= 1440 };
  }

  // 把时间转成北京时间的 { ymd, md, hm }；非法或零值（年份 < 2000）返回 null。
  function bjParts(iso) {
    if (!iso) return null;
    const t = new Date(iso).getTime();
    if (isNaN(t) || new Date(t).getUTCFullYear() < 2000) return null;
    const d = new Date(t + 8 * 3600 * 1000);
    const pad = (n) => String(n).padStart(2, "0");
    const md = pad(d.getUTCMonth() + 1) + "-" + pad(d.getUTCDate());
    return { ymd: d.getUTCFullYear() + "-" + md, md, hm: pad(d.getUTCHours()) + ":" + pad(d.getUTCMinutes()), t };
  }

  // 账号页顶部的定时检查状态文案。s 是 /api/schedule/status 的返回，nowMs 是当前毫秒时间。
  function scheduleLine(s, nowMs) {
    if (!s || !s.enabled) return "定时检查未开启";
    const now = bjParts(nowMs);
    const last = bjParts(s.last_run_at);
    const done = (s.ok || 0) + (s.failed || 0) + (s.skipped || 0);
    let head;
    if (!last) {
      head = "上次定时检查：尚未运行";
    } else if (s.total > 0 && done < s.total && nowMs - last.t < 6 * 3600 * 1000) {
      head = `定时检查进行中（${done}/${s.total}）`;
    } else {
      head = `上次定时检查：${last.md} ${last.hm}（北京时间），成功 ${s.ok || 0} / 失败 ${s.failed || 0}`;
    }
    const next = bjParts(s.next_run_at);
    if (!next) return head;
    let day = next.md;
    if (now) {
      const dayMs = 24 * 3600 * 1000;
      const diff = Math.round((Date.parse(next.ymd) - Date.parse(now.ymd)) / dayMs);
      if (diff === 0) day = "今天";
      else if (diff === 1) day = "明天";
    }
    return `${head}；下次：${day} ${next.hm}`;
  }

  const api = { parseSSE, reconnectDelay, mergeEventAccount, estimateMinutes, windowEstimate, scheduleLine };
  root.LiveUI = api;
  if (typeof module !== "undefined" && module.exports) module.exports = api;
})(typeof globalThis !== "undefined" ? globalThis : this);
