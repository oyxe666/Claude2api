"use strict";
/*
 * 乱葬岗页面用到的纯函数，无依赖，可在 Node 里测试。
 * 所有 DOM 都用 createElement + textContent 构造，邮箱等内容不会被当作 HTML 解释。
 */
(function (root) {
  const REASONS = {
    session_invalid: "会话失效",
    auto_removed: "自动清理",
    manual_delete: "手动删除",
  };

  function reasonLabel(reason) {
    return REASONS[reason] || "未知原因";
  }

  // 存活时长：<1 分钟、N 分钟、N 小时、N 天（再带 M 小时）。
  function fmtLifespan(sec) {
    const s = Number(sec);
    if (!Number.isFinite(s) || s < 0) return "—";
    if (s < 60) return "不到 1 分钟";
    if (s < 3600) return Math.floor(s / 60) + " 分钟";
    if (s < 86400) return Math.floor(s / 3600) + " 小时";
    const d = Math.floor(s / 86400);
    const h = Math.floor((s % 86400) / 3600);
    return h ? `${d} 天 ${h} 小时` : `${d} 天`;
  }

  const pct = (w) => (w && typeof w.utilization === "number" && Number.isFinite(w.utilization) ? Math.round(w.utilization * 100) + "%" : "—");

  // 死前额度：utilization 为 0~1 小数。没有快照时返回「无额度记录」。
  function quotaText(q) {
    if (!q || (!q.five_hour && !q.seven_day)) return "无额度记录";
    return `5 小时用了 ${pct(q.five_hour)} · 7 天用了 ${pct(q.seven_day)}`;
  }

  // 一句克制的墓志铭。
  function epitaph(g) {
    const life = fmtLifespan(g && g.lifespan_sec);
    const n = (g && g.chat_count) || 0;
    const chats = n > 0 ? `生前陪聊 ${n} 次` : "还没来得及干活";
    switch (g && g.reason) {
      case "manual_delete":
        return `被请出号池，在 ${life} 后离场，${chats}。`;
      case "auto_removed":
        return `会话失效，被自动清理，在 ${life} 后下线，${chats}。`;
      default:
        return `在 ${life} 后下线，${chats}。`;
    }
  }

  function el(doc, tag, cls, text) {
    const e = doc.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = text;
    return e;
  }

  // 顶部统计：返回 span 数组，调用方清空容器后逐个 append。
  function buildStats(doc, stats) {
    const s = stats || {};
    const item = (label, value) => {
      const span = el(doc, "span");
      span.appendChild(el(doc, "small", "", label));
      span.appendChild(el(doc, "b", "", value));
      return span;
    };
    return [
      item("累计阵亡", String(s.total || 0)),
      item("平均寿命", s.total ? fmtLifespan(s.avg_lifespan_sec) : "—"),
      item("近 7 天阵亡", String(s.last_7_days || 0)),
    ];
  }

  // 墓碑卡片。opts.fmtTime 可自定义时间格式。
  function buildTomb(doc, g, opts) {
    const fmt = (opts && opts.fmtTime) || ((t) => (t ? new Date(t).toLocaleString() : "—"));
    const card = el(doc, "div", "grave-card");
    card.appendChild(el(doc, "div", "grave-email", g.email));
    card.appendChild(el(doc, "span", "grave-reason reason-" + g.reason, reasonLabel(g.reason)));
    card.appendChild(el(doc, "div", "grave-line", "阵亡时间：" + fmt(g.died_at)));
    card.appendChild(el(doc, "div", "grave-line", "存活：" + fmtLifespan(g.lifespan_sec)));
    card.appendChild(el(doc, "div", "grave-line", "对话：" + (g.chat_count || 0) + " 次"));
    card.appendChild(el(doc, "div", "grave-line", "临终额度：" + quotaText(g.last_quota)));
    card.appendChild(el(doc, "div", "grave-epitaph", epitaph(g)));
    return card;
  }

  function buildEmpty(doc) {
    return el(doc, "div", "empty", "还没有账号阵亡");
  }

  const api = { reasonLabel, fmtLifespan, quotaText, epitaph, buildStats, buildTomb, buildEmpty };
  root.GraveUI = api;
  if (typeof module !== "undefined" && module.exports) module.exports = api;
})(typeof globalThis !== "undefined" ? globalThis : this);
