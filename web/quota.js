"use strict";
/*
 * 额度展示：纯函数 + 纯 DOM 构建，无依赖。
 * - 内部 utilization 一律 0~1 小数，resets_at 一律 unix 秒；展示层只在这里换算成「剩余百分比」。
 * - 所有“现在”都由参数 nowMs 传入，便于 Node 测试。
 * - DOM 只用 createElement + textContent，不拼 innerHTML。
 */
(function (root) {
  const MIN = 60 * 1000;
  const HOUR = 60 * MIN;
  const DAY = 24 * HOUR;
  const NOTE = "按账号等权平均，仅供参考";
  const EMPTY_HINT = "暂无数据，点击刷新";

  function clamp(n, lo, hi) {
    return Math.min(hi, Math.max(lo, n));
  }

  function toNum(v) {
    if (v === null || v === undefined || v === "") return null;
    const n = Number(v);
    return Number.isFinite(n) ? n : null;
  }

  // 解析时间字符串；空、非法、0001- 开头(Go 零值)都视为没有
  function parseTime(t) {
    if (!t || typeof t !== "string") return null;
    if (t.startsWith("0001-")) return null;
    const ms = Date.parse(t);
    return Number.isNaN(ms) ? null : ms;
  }

  // 单个窗口 → {state: nodata|ok|exhausted|reset, remaining: 0~100|null, resetsAtMs}
  function windowState(w, nowMs) {
    const none = { state: "nodata", remaining: null, resetsAtMs: null };
    if (!w || typeof w !== "object") return none;
    const resetsSec = toNum(w.resets_at);
    const resetsAtMs = resetsSec && resetsSec > 0 ? resetsSec * 1000 : null;
    const util = toNum(w.utilization);
    const exceeded = String(w.status || "").includes("exceeded");

    // 重置时间已过：旧用量不再有意义，按满额
    if (resetsAtMs !== null && resetsAtMs <= nowMs) {
      return { state: "reset", remaining: 100, resetsAtMs };
    }
    if (util === null) {
      if (exceeded) return { state: "exhausted", remaining: 0, resetsAtMs };
      return none;
    }
    const used = clamp(util, 0, 1);
    if (exceeded || used >= 1) {
      return { state: "exhausted", remaining: 0, resetsAtMs };
    }
    // 保留 1 位小数，消除 100 - 0.07*100 这类浮点噪声
    const remaining = Math.round((100 - used * 100) * 10) / 10;
    return { state: "ok", remaining, resetsAtMs };
  }

  function accountView(acct, nowMs) {
    const a = acct || {};
    const q = a.quota || null;
    const five = windowState(q && q.five_hour, nowMs);
    const seven = windowState(q && q.seven_day, nowMs);
    const usable = a.has_session !== false && a.status !== "expired";
    const hasData = five.state !== "nodata" || seven.state !== "nodata";
    const exhaustedWins = [five, seven].filter((w) => w.state === "exhausted");
    const exhausted = usable && exhaustedWins.length > 0;

    // 要全部耗尽的窗口都恢复才可用 → 取最大；任一没有重置时间则未知
    let recoverAtMs = null;
    if (exhausted && exhaustedWins.every((w) => w.resetsAtMs !== null)) {
      recoverAtMs = Math.max(...exhaustedWins.map((w) => w.resetsAtMs));
    }

    const updatedAtMs = parseTime(q && q.updated_at);
    const errorAtMs = parseTime(q && q.error_at);
    const failMsg = (q && q.error) || "";
    const failed = !!failMsg && errorAtMs !== null && errorAtMs > (updatedAtMs || 0);

    return {
      email: String(a.email || ""),
      usable,
      expired: a.status === "expired",
      five,
      seven,
      hasData,
      exhausted,
      recoverAtMs,
      failed,
      failMsg: failed ? failMsg : "",
      updatedAtMs,
    };
  }

  function summarize(accounts, nowMs) {
    const list = Array.isArray(accounts) ? accounts : [];
    const s = {
      total: list.length,
      available: 0,
      avg5: null,
      avg7: null,
      n5: 0,
      n7: 0,
      hasAnyData: false,
      unavailable: 0,
      nextRecovery: null,
      recoveryUnknown: false,
    };
    let sum5 = 0;
    let sum7 = 0;
    const recoverTimes = [];
    for (const a of list) {
      const v = accountView(a, nowMs);
      if (!v.usable) continue; // 失效账号不入平均、不算可用
      if (v.hasData) s.hasAnyData = true;
      if (v.five.state !== "nodata") {
        sum5 += v.five.remaining;
        s.n5++;
      }
      if (v.seven.state !== "nodata") {
        sum7 += v.seven.remaining;
        s.n7++;
      }
      if (v.exhausted) {
        s.unavailable++;
        if (v.recoverAtMs !== null) recoverTimes.push(v.recoverAtMs);
      } else {
        s.available++;
      }
    }
    if (s.n5) s.avg5 = sum5 / s.n5;
    if (s.n7) s.avg7 = sum7 / s.n7;
    if (recoverTimes.length) {
      const min = Math.min(...recoverTimes);
      s.nextRecovery = {
        atMs: min,
        count: recoverTimes.filter((t) => t <= min + MIN).length,
      };
    } else if (s.unavailable > 0) {
      s.recoveryUnknown = true;
    }
    return s;
  }

  // >50 高；20~50(含) 中；<20 低；无数据 none
  function colorLevel(remaining) {
    if (remaining === null || remaining === undefined || Number.isNaN(remaining)) return "none";
    if (remaining > 50) return "high";
    if (remaining >= 20) return "mid";
    return "low";
  }

  function fmtCountdown(ms) {
    if (!(ms >= MIN)) return "不到 1 分钟";
    if (ms >= DAY) {
      const d = Math.floor(ms / DAY);
      const h = Math.floor((ms % DAY) / HOUR);
      return h ? `${d} 天 ${h} 小时` : `${d} 天`;
    }
    if (ms >= HOUR) {
      const h = Math.floor(ms / HOUR);
      const m = Math.floor((ms % HOUR) / MIN);
      return m ? `${h} 小时 ${m} 分` : `${h} 小时`;
    }
    return `${Math.floor(ms / MIN)} 分钟`;
  }

  function fmtAgo(thenMs, nowMs) {
    if (thenMs === null || thenMs === undefined || Number.isNaN(thenMs)) return "暂无";
    const d = nowMs - thenMs;
    if (d < MIN) return "刚刚";
    if (d < HOUR) return `${Math.floor(d / MIN)} 分钟前`;
    if (d < DAY) return `${Math.floor(d / HOUR)} 小时前`;
    return `${Math.floor(d / DAY)} 天前`;
  }

  // 本地时区 MM-DD HH:mm
  function fmtLocalTime(ms) {
    const d = new Date(ms);
    const p = (n) => String(n).padStart(2, "0");
    return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
  }

  function fmtPct(remaining) {
    return `${Math.round(remaining)}%`;
  }

  function recoverText(atMs, nowMs) {
    const left = atMs - nowMs;
    return left < MIN ? "即将恢复" : `${fmtCountdown(left)}后恢复`;
  }

  function overviewText(summary, nowMs) {
    const s = summary;
    const out = {
      available: `${s.available}/${s.total}`,
      avg5: s.avg5 === null ? "—" : fmtPct(s.avg5),
      avg7: s.avg7 === null ? "—" : fmtPct(s.avg7),
      recovery: "—",
      note: NOTE,
      empty: s.hasAnyData ? "" : EMPTY_HINT,
    };
    if (s.nextRecovery) {
      out.recovery = `${recoverText(s.nextRecovery.atMs, nowMs)} ${s.nextRecovery.count} 个号`;
    } else if (s.recoveryUnknown) {
      out.recovery = "恢复时间未知";
    } else if (s.hasAnyData) {
      out.recovery = "全部可用";
    }
    return out;
  }

  // ---------- DOM（只用 createElement + textContent） ----------

  function el(doc, tag, cls, text) {
    const e = doc.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined && text !== null) e.textContent = String(text);
    return e;
  }

  function buildBar(doc, remaining, ariaLabel) {
    const bar = el(doc, "div", "quota-bar");
    bar.setAttribute("role", "progressbar");
    bar.setAttribute("aria-valuemin", "0");
    bar.setAttribute("aria-valuemax", "100");
    bar.setAttribute("aria-label", ariaLabel);
    const fill = el(doc, "div", "quota-fill bar-" + colorLevel(remaining));
    if (remaining === null || remaining === undefined) {
      bar.setAttribute("aria-valuetext", "暂无数据");
      fill.style.width = "0%";
    } else {
      const now = Math.round(clamp(remaining, 0, 100));
      bar.setAttribute("aria-valuenow", String(now));
      fill.style.width = now + "%";
    }
    bar.appendChild(fill);
    return bar;
  }

  function buildOverview(doc, summary, opts) {
    const o = opts || {};
    const nowMs = o.nowMs;
    const t = overviewText(summary, nowMs);
    const batch = o.batch || null;
    const running = !!(batch && batch.running);

    const box = el(doc, "div", "quota-overview-box");

    const metrics = el(doc, "div", "quota-metrics");
    const add = (label, value, level, bar) => {
      const m = el(doc, "div", "quota-metric");
      m.appendChild(el(doc, "span", "quota-metric-label", label));
      m.appendChild(el(doc, "span", "quota-metric-value" + (level ? " lv-" + level : ""), value));
      if (bar !== undefined) m.appendChild(bar);
      metrics.appendChild(m);
    };
    add("可用账号", t.available, "");
    add("5 小时剩余", t.avg5, colorLevel(summary.avg5), buildBar(doc, summary.avg5, "5 小时平均剩余额度"));
    add("7 天剩余", t.avg7, colorLevel(summary.avg7), buildBar(doc, summary.avg7, "7 天平均剩余额度"));
    add("最近恢复", t.recovery, "");
    box.appendChild(metrics);

    const foot = el(doc, "div", "quota-overview-foot");
    foot.appendChild(el(doc, "span", "quota-note", t.empty || t.note));
    const act = el(doc, "div", "quota-overview-act");
    const prog = el(doc, "span", "quota-progress", batch && batch.total ? `${batch.done}/${batch.total}` : "");
    prog.setAttribute("role", "status");
    act.appendChild(prog);
    const btn = el(doc, "button", "btn btn-primary quota-all-btn", running ? "刷新中…" : "刷新全部额度");
    btn.setAttribute("type", "button");
    btn.setAttribute("data-act", "refresh-all-quota");
    if (running) {
      btn.disabled = true;
      btn.setAttribute("aria-busy", "true");
    }
    act.appendChild(btn);
    foot.appendChild(act);
    box.appendChild(foot);
    return box;
  }

  function buildRow(doc, label, w, nowMs) {
    const row = el(doc, "div", "quota-row");
    const head = el(doc, "div", "quota-row-head");
    head.appendChild(el(doc, "span", "quota-label", label));
    const val = w.state === "nodata" ? "暂无" : `剩余 ${fmtPct(w.remaining)}`;
    head.appendChild(el(doc, "span", "quota-val lv-" + colorLevel(w.remaining), val));
    row.appendChild(head);
    row.appendChild(buildBar(doc, w.remaining, `${label}剩余额度`));
    let sub;
    if (w.state === "nodata") sub = "暂无";
    else if (w.state === "reset") sub = "已重置";
    else if (w.resetsAtMs === null) sub = "重置时间未知";
    else sub = `${fmtCountdown(w.resetsAtMs - nowMs)}后重置 · ${fmtLocalTime(w.resetsAtMs)}`;
    row.appendChild(el(doc, "div", "quota-sub", sub));
    return row;
  }

  function buildCard(doc, acct, nowMs, opts) {
    const o = opts || {};
    const v = accountView(acct, nowMs);
    const cls = ["quota-card"];
    if (v.exhausted) cls.push("is-exhausted");
    if (v.failed) cls.push("is-failed");
    if (v.expired) cls.push("is-expired");
    const card = el(doc, "div", cls.join(" "));
    card.setAttribute("data-email", v.email);

    const head = el(doc, "div", "quota-card-head");
    const email = el(doc, "span", "quota-email mono", v.email);
    email.setAttribute("title", v.email);
    head.appendChild(email);
    const badges = el(doc, "span", "quota-badges");
    if (v.expired) badges.appendChild(el(doc, "span", "quota-badge b-expired", "已失效"));
    if (v.exhausted) {
      const txt = v.recoverAtMs === null ? "已耗尽" : `已耗尽 · ${recoverText(v.recoverAtMs, nowMs)}`;
      badges.appendChild(el(doc, "span", "quota-badge b-exhausted", txt));
    }
    if (v.failed) badges.appendChild(el(doc, "span", "quota-badge b-failed", "失败"));
    head.appendChild(badges);

    const loading = !!(o.refreshing && o.refreshing(v.email));
    const rb = el(doc, "button", "quota-refresh" + (loading ? " is-loading" : ""));
    rb.setAttribute("type", "button");
    rb.setAttribute("data-act", "refresh-quota");
    rb.setAttribute("aria-label", `刷新 ${v.email} 的额度`);
    rb.setAttribute("title", "刷新额度");
    if (loading) rb.setAttribute("aria-busy", "true");
    // 批次进行中，没在转圈的卡片也不允许单独刷新
    if (loading || o.locked) rb.disabled = true;
    rb.appendChild(el(doc, "span", "quota-spin"));
    head.appendChild(rb);
    card.appendChild(head);

    card.appendChild(buildRow(doc, "5 小时", v.five, nowMs));
    card.appendChild(buildRow(doc, "7 天", v.seven, nowMs));

    if (v.failed) card.appendChild(el(doc, "div", "quota-fail", "额度查询失败：" + v.failMsg));

    const foot = el(doc, "div", "quota-card-foot");
    const acts = el(doc, "span", "quota-actions");
    for (const [act, text, extra] of [
      ["refresh-acct", "刷新账号", ""],
      ["detail", "详情", ""],
      ["delete", "删除", " btn-danger"],
    ]) {
      const b = el(doc, "button", "btn-sm" + extra, text);
      b.setAttribute("type", "button");
      b.setAttribute("data-act", act);
      acts.appendChild(b);
    }
    foot.appendChild(acts);
    foot.appendChild(el(doc, "span", "quota-updated", "更新于 " + fmtAgo(v.updatedAtMs, nowMs)));
    card.appendChild(foot);
    return card;
  }

  const api = {
    windowState,
    accountView,
    summarize,
    colorLevel,
    fmtCountdown,
    fmtAgo,
    fmtLocalTime,
    overviewText,
    buildOverview,
    buildCard,
    NOTE,
    EMPTY_HINT,
  };
  root.QuotaUI = api;
  if (typeof module !== "undefined" && module.exports) module.exports = api;
})(typeof globalThis !== "undefined" ? globalThis : this);
