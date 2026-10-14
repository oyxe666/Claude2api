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

  // 图表：纯 SVG，用 createElementNS 构造，不拼 HTML。
  const SVGNS = "http://www.w3.org/2000/svg";
  const CHART_W = 640, CHART_H = 220;

  function chartPoints(daily, w, h, pad) {
    const n = daily.length;
    const max = Math.max(1, ...daily.map((d) => Number(d.count) || 0));
    const iw = w - pad.l - pad.r;
    const ih = h - pad.t - pad.b;
    return daily.map((d, i) => ({
      date: d.date,
      count: Number(d.count) || 0,
      x: pad.l + (n <= 1 ? iw / 2 : (iw * i) / (n - 1)),
      y: pad.t + ih - (ih * (Number(d.count) || 0)) / max,
    }));
  }

  function svgEl(doc, tag, attrs) {
    const e = doc.createElementNS(SVGNS, tag);
    for (const k in attrs || {}) e.setAttribute(k, String(attrs[k]));
    return e;
  }

  // 网格线 + 左侧刻度。values 为刻度数值数组（自上而下），纵坐标由 yOf 计算。
  function drawGrid(doc, svg, pad, w, h, labels) {
    const ih = h - pad.t - pad.b;
    const n = 4;
    for (let i = 0; i <= n; i++) {
      const y = pad.t + (ih * i) / n;
      svg.appendChild(svgEl(doc, "line", { x1: pad.l, y1: y.toFixed(1), x2: w - pad.r, y2: y.toFixed(1), class: "chart-grid" }));
      const t = svgEl(doc, "text", { x: pad.l - 6, y: (y + 4).toFixed(1), "text-anchor": "end", class: "chart-label" });
      t.textContent = labels[n - i];
      svg.appendChild(t);
    }
  }

  // 面积：折线点 + 底部两个角。
  function areaPolygon(doc, pts, h, pad, cls) {
    const base = (h - pad.b).toFixed(1);
    const line = pts.map((p) => p.x.toFixed(1) + "," + p.y.toFixed(1));
    const first = pts[0].x.toFixed(1), last = pts[pts.length - 1].x.toFixed(1);
    return svgEl(doc, "polygon", { points: line.concat([last + "," + base, first + "," + base]).join(" "), class: cls });
  }

  // 横轴标签：7 天每点都标，30 天每隔 5 点标；首尾必标。
  function drawDateLabels(doc, svg, pts, h, every) {
    const last = pts.length - 1;
    pts.forEach((p, i) => {
      const isEdge = i === 0 || i === last;
      if (!isEdge && i % every !== 0) return;
      const anchor = i === 0 ? "start" : i === last ? "end" : "middle";
      const l = svgEl(doc, "text", { x: p.x.toFixed(1), y: h - 8, "text-anchor": anchor, class: "chart-label" });
      l.textContent = p.date.slice(5);
      svg.appendChild(l);
    });
  }

  function buildChart(doc, daily) {
    const box = el(doc, "div", "grave-chart");
    const list = Array.isArray(daily) ? daily : [];
    if (!list.some((d) => Number(d.count) > 0)) {
      box.appendChild(el(doc, "div", "empty", "这段时间没有账号阵亡"));
      return box;
    }
    const w = CHART_W, h = CHART_H, pad = { l: 32, r: 12, t: 12, b: 28 };
    const pts = chartPoints(list, w, h, pad);
    const max = Math.max(1, ...pts.map((p) => p.count));
    const labels = [0, 1, 2, 3, 4].map((i) => String(Math.round((max * i) / 4)));
    const svg = svgEl(doc, "svg", { viewBox: `0 0 ${w} ${h}`, role: "img", "aria-label": "每日阵亡数折线图", class: "grave-chart-svg" });
    drawGrid(doc, svg, pad, w, h, labels);
    svg.appendChild(svgEl(doc, "line", { x1: pad.l, y1: h - pad.b, x2: w - pad.r, y2: h - pad.b, class: "chart-axis" }));
    svg.appendChild(areaPolygon(doc, pts, h, pad, "chart-area"));
    svg.appendChild(svgEl(doc, "polyline", { points: pts.map((p) => p.x.toFixed(1) + "," + p.y.toFixed(1)).join(" "), class: "chart-line", fill: "none" }));
    pts.forEach((p) => {
      const c = svgEl(doc, "circle", { cx: p.x.toFixed(1), cy: p.y.toFixed(1), r: 4, class: "chart-dot", tabindex: 0 });
      const t = svgEl(doc, "title", {});
      t.textContent = p.date + "：" + p.count + " 个";
      c.appendChild(t);
      svg.appendChild(c);
    });
    drawDateLabels(doc, svg, pts, h, pts.length > 14 ? 5 : 1);
    box.appendChild(svg);
    return box;
  }

  // ---- 存活统计 ----
  const pctText = (r) => (Number.isFinite(r) ? Math.round(r * 100) + "%" : "—");

  // 存活率概览：返回 span 数组。口径不足（分母为 0）显示“—”。
  function buildSurvivalSummary(doc, s) {
    const x = s || {};
    const item = (label, value) => {
      const span = el(doc, "span");
      span.appendChild(el(doc, "small", "", label));
      span.appendChild(el(doc, "b", "", value));
      return span;
    };
    return [
      item("累计导入", String(x.imported || 0)),
      item("当前存活率", x.imported ? pctText(x.alive_rate) : "—"),
      item("活过 7 天", x.eligible_7 ? pctText(x.rate_7) + `（${x.survived_7}/${x.eligible_7}）` : "—"),
      item("活过 30 天", x.eligible_30 ? pctText(x.rate_30) + `（${x.survived_30}/${x.eligible_30}）` : "—"),
    ];
  }

  // 存活曲线点：横轴天数，纵轴 0~1。
  function curvePoints(curve, w, h, pad) {
    const n = curve.length;
    const iw = w - pad.l - pad.r;
    const ih = h - pad.t - pad.b;
    return curve.map((p, i) => {
      const sv = Number.isFinite(Number(p.survival)) ? Number(p.survival) : 0;
      return { day: p.day, survival: sv, x: pad.l + (n <= 1 ? iw / 2 : (iw * i) / (n - 1)), y: pad.t + ih - ih * sv };
    });
  }

  function buildSurvivalCurve(doc, curve) {
    const box = el(doc, "div", "grave-chart");
    const list = Array.isArray(curve) ? curve : [];
    if (!list.length) {
      box.appendChild(el(doc, "div", "empty", "还没有足够的数据画存活曲线"));
      return box;
    }
    const w = CHART_W, h = CHART_H, pad = { l: 40, r: 12, t: 12, b: 28 };
    const pts = curvePoints(list, w, h, pad);
    const svg = svgEl(doc, "svg", { viewBox: `0 0 ${w} ${h}`, role: "img", "aria-label": "账号存活曲线", class: "grave-chart-svg" });
    drawGrid(doc, svg, pad, w, h, ["0%", "25%", "50%", "75%", "100%"]);
    svg.appendChild(svgEl(doc, "line", { x1: pad.l, y1: h - pad.b, x2: w - pad.r, y2: h - pad.b, class: "chart-axis" }));
    svg.appendChild(areaPolygon(doc, pts, h, pad, "chart-area"));
    svg.appendChild(svgEl(doc, "polyline", { points: pts.map((p) => p.x.toFixed(1) + "," + p.y.toFixed(1)).join(" "), class: "chart-line", fill: "none" }));
    pts.forEach((p, i) => {
      const c = svgEl(doc, "circle", { cx: p.x.toFixed(1), cy: p.y.toFixed(1), r: 3, class: "chart-dot", tabindex: 0 });
      const t = svgEl(doc, "title", {});
      t.textContent = `第 ${p.day} 天：${Math.round(p.survival * 100)}% 存活`;
      c.appendChild(t);
      svg.appendChild(c);
      if (i === 0 || i === pts.length - 1) {
        const l = svgEl(doc, "text", { x: p.x.toFixed(1), y: h - 8, "text-anchor": i === 0 ? "start" : "end", class: "chart-label" });
        l.textContent = "第 " + p.day + " 天";
        svg.appendChild(l);
      }
    });
    box.appendChild(svg);
    return box;
  }

  // 每日导入 vs 阵亡双线图。daily = [{date, imported, died}]。
  function dualPoints(daily, w, h, pad) {
    const n = daily.length;
    const val = (d, k) => Number(d[k]) || 0;
    const max = Math.max(1, ...daily.map((d) => Math.max(val(d, "imported"), val(d, "died"))));
    const iw = w - pad.l - pad.r;
    const ih = h - pad.t - pad.b;
    const x = (i) => pad.l + (n <= 1 ? iw / 2 : (iw * i) / (n - 1));
    const y = (v) => pad.t + ih - (ih * v) / max;
    return {
      max,
      imported: daily.map((d, i) => ({ date: d.date, v: val(d, "imported"), x: x(i), y: y(val(d, "imported")) })),
      died: daily.map((d, i) => ({ date: d.date, v: val(d, "died"), x: x(i), y: y(val(d, "died")) })),
    };
  }

  function buildDualChart(doc, daily) {
    const box = el(doc, "div", "grave-chart");
    const list = Array.isArray(daily) ? daily : [];
    if (!list.some((d) => Number(d.imported) > 0 || Number(d.died) > 0)) {
      box.appendChild(el(doc, "div", "empty", "这段时间没有导入或阵亡"));
      return box;
    }
    const w = CHART_W, h = CHART_H, pad = { l: 32, r: 12, t: 12, b: 28 };
    const dp = dualPoints(list, w, h, pad);
    const labels = [0, 1, 2, 3, 4].map((i) => String(Math.round((dp.max * i) / 4)));
    const svg = svgEl(doc, "svg", { viewBox: `0 0 ${w} ${h}`, role: "img", "aria-label": "每日导入与阵亡对比", class: "grave-chart-svg" });
    drawGrid(doc, svg, pad, w, h, labels);
    svg.appendChild(svgEl(doc, "line", { x1: pad.l, y1: h - pad.b, x2: w - pad.r, y2: h - pad.b, class: "chart-axis" }));
    [
      ["imported", "chart-area chart-area-imported", "chart-line chart-line-imported", "导入"],
      ["died", "chart-area chart-area-dead", "chart-line chart-line-dead", "阵亡"],
    ].forEach(([k, areaCls, lineCls, name]) => {
      svg.appendChild(areaPolygon(doc, dp[k], h, pad, areaCls));
      svg.appendChild(svgEl(doc, "polyline", { points: dp[k].map((p) => p.x.toFixed(1) + "," + p.y.toFixed(1)).join(" "), class: lineCls, fill: "none" }));
      dp[k].forEach((p) => {
        let c;
        if (k === "died") {
          c = svgEl(doc, "rect", { x: (p.x - 3.5).toFixed(1), y: (p.y - 3.5).toFixed(1), width: 7, height: 7, class: "chart-dot chart-dot-dead", tabindex: 0 });
        } else {
          c = svgEl(doc, "circle", { cx: p.x.toFixed(1), cy: p.y.toFixed(1), r: 3, class: "chart-dot chart-dot-imported", tabindex: 0 });
        }
        const t = svgEl(doc, "title", {});
        t.textContent = p.date + "：" + name + " " + p.v + " 个";
        c.appendChild(t);
        svg.appendChild(c);
      });
    });
    drawDateLabels(doc, svg, dp.imported, h, list.length > 14 ? 5 : 1);
    box.appendChild(svg);

    const legend = el(doc, "div", "chart-legend");
    const lgImp = el(doc, "span");
    lgImp.appendChild(el(doc, "i"));
    lgImp.appendChild(doc.createTextNode("导入"));
    const lgDead = el(doc, "span", "lg-dead");
    lgDead.appendChild(el(doc, "i"));
    lgDead.appendChild(doc.createTextNode("阵亡"));
    legend.appendChild(lgImp);
    legend.appendChild(lgDead);
    box.appendChild(legend);
    return box;
  }

  const api = { reasonLabel, fmtLifespan, quotaText, epitaph, buildStats, buildTomb, buildEmpty, buildChart, chartPoints, buildSurvivalSummary, curvePoints, buildSurvivalCurve, dualPoints, buildDualChart };
  root.GraveUI = api;
  if (typeof module !== "undefined" && module.exports) module.exports = api;
})(typeof globalThis !== "undefined" ? globalThis : this);
