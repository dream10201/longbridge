const initialStatus = JSON.parse(document.getElementById("initial-status").textContent);
const openPollIntervalMs = 5000;
const closedPollIntervalMs = 30000;
const displayTimeZone = "Asia/Shanghai";

function buildStatusURL() {
  const path = window.location.pathname || "/";
  const normalized = path.endsWith("/") ? path : path.substring(0, path.lastIndexOf("/") + 1);
  return new URL("api/status", window.location.origin + normalized).toString();
}

function esc(value) {
  return String(value ?? "").replace(/[&<>"']/g, (ch) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;"
  }[ch]));
}

function fmtDate(value) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return esc(value);
  return date.toLocaleString("zh-CN", { hour12: false, timeZone: displayTimeZone });
}

function fmtTime(value) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return esc(value);
  return date.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false, timeZone: displayTimeZone });
}

function fmtShortDate(value) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return esc(value);
  return date.toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false, timeZone: displayTimeZone });
}

function toNumber(value) {
  if (value === null || value === undefined || value === "") return null;
  const num = Number(String(value).replace(/,/g, "").trim());
  return Number.isFinite(num) ? num : null;
}

function fmtNumber(value, digits = 2) {
  if (value === null || value === undefined || value === "") return "-";
  const num = toNumber(value);
  if (num === null) return esc(value);
  return num.toLocaleString("zh-CN", { minimumFractionDigits: digits, maximumFractionDigits: digits });
}

function fmtSigned(value, digits = 2) {
  if (value === null || value === undefined || Number.isNaN(value)) return "-";
  return (value > 0 ? "+" : "") + fmtNumber(value, digits);
}

function fmtPct(value) {
  return value === null ? "" : fmtSigned(value) + "%";
}

function pctDistance(current, target) {
  if (current === null || target === null || target === 0) return null;
  return ((current - target) / target) * 100;
}

function resolveSide(stock, side) {
  const buy = side === "buy";
  const base = toNumber(buy ? (stock.base_buy_price || stock.next_buy_price) : (stock.base_sell_price || stock.next_sell_price));
  const armed = buy ? !!stock.buy_trail_armed : !!stock.sell_trail_armed;
  const trail = armed ? toNumber(buy ? stock.buy_trail_trigger : stock.sell_trail_trigger) : null;
  const current = toNumber(stock.price);
  const effective = trail !== null ? trail : base;
  const trailWins = trail !== null && base !== null && Math.abs(trail - base) > 0.0000001;
  const delta = current !== null && effective !== null ? current - effective : null;
  return {
    base,
    trail,
    armed,
    effective,
    source: trailWins ? "追踪价" : "基础价",
    delta,
    deltaPct: pctDistance(current, effective),
    tone: delta === null ? "" : (buy ? delta <= 0 : delta >= 0) ? "ok" : "muted",
  };
}

let pollFailureCount = 0;
let latestStatus = initialStatus;
let stockFilter = "all";
let previousStatus = null;
let eventStream = [];

function marketPhaseText(phase) {
  switch (phase) {
    case "pre": return "盘前";
    case "normal": return "常规盘";
    case "post": return "盘后";
    case "overnight": return "夜盘";
    case "closed": return "休市";
    default: return phase || "-";
  }
}

function actionText(action) {
  switch (action) {
    case "buy": return "买入";
    case "sell": return "卖出";
    default: return action || "-";
  }
}

function limitedSymbols(status) {
  return new Set(((status.summary || {}).limited_symbols || []).map(String));
}

function activeSymbols(status) {
  return (status.symbols || []).filter((stock) => stock.enabled !== false);
}

function disabledSymbols(status) {
  return (status.symbols || []).filter((stock) => stock.enabled === false);
}

function stockMatchesFilter(stock, limited) {
  if (stockFilter === "pending") return !!stock.pending_order_id;
  if (stockFilter === "limited") return limited.has(stock.symbol);
  if (stockFilter === "error") return !!stock.last_error;
  return true;
}

function nearestTrigger(buy, sell) {
  const candidates = [];
  if (buy.deltaPct !== null) candidates.push({ side: "买入", pct: Math.abs(buy.deltaPct), delta: buy.delta, value: buy.effective });
  if (sell.deltaPct !== null) candidates.push({ side: "卖出", pct: Math.abs(sell.deltaPct), delta: sell.delta, value: sell.effective });
  candidates.sort((a, b) => a.pct - b.pct);
  return candidates[0] || null;
}

function stockState(stock, limited) {
  const buy = resolveSide(stock, "buy");
  const sell = resolveSide(stock, "sell");
  if (stock.last_error) return { kind: "danger", label: "异常", text: stock.last_error, buy, sell };
  if (stock.pending_order_id) {
    const price = toNumber(stock.price);
    const pending = toNumber(stock.pending_price);
    const gap = price !== null && pending !== null ? price - pending : null;
    const text = actionText(stock.pending_side) + " " + fmtNumber(stock.pending_price) + " × " + esc(stock.order_quantity || "-") +
      "，价差 " + fmtSigned(gap) + (gap === null ? "" : " (" + fmtPct(pctDistance(price, pending)) + ")") +
      (stock.pending_timeout_left ? "，超时 " + stock.pending_timeout_left : "");
    return { kind: "pending", label: "挂单 · " + actionText(stock.pending_side), text, buy, sell };
  }
  if (limited.has(stock.symbol)) return { kind: "warn", label: "买入受限", text: (stock.buy_capacity_bottlenecks || []).join("、") || stock.buy_capacity_reason || "", buy, sell };
  if (buy.armed) return { kind: "buy", label: "追踪买入", text: "反弹到 " + fmtNumber(buy.effective) + " 买入，距离 " + fmtSigned(buy.delta) + " (" + fmtPct(buy.deltaPct) + ")", buy, sell };
  if (sell.armed) return { kind: "sell", label: "追踪止盈", text: "回撤到 " + fmtNumber(sell.effective) + " 卖出，距离 " + fmtSigned(sell.delta) + " (" + fmtPct(sell.deltaPct) + ")", buy, sell };
  const nearest = nearestTrigger(buy, sell);
  const text = nearest
    ? "距" + nearest.side + "触发 " + fmtNumber(nearest.value) + " 还差 " + fmtSigned(nearest.delta) + " (" + fmtSigned(nearest.delta < 0 ? -nearest.pct : nearest.pct) + "%)"
    : "暂无触发条件";
  return { kind: "idle", label: "等待", text, buy, sell };
}

function urgencyScore(stock, state) {
  let score = { danger: 1000, pending: 800, warn: 500, buy: 300, sell: 300, idle: 0 }[state.kind] || 0;
  const distances = [state.buy.deltaPct, state.sell.deltaPct].filter((v) => v !== null).map(Math.abs);
  if (distances.length) {
    const nearest = Math.min(...distances);
    if (nearest <= 0.5) score += 220;
    else if (nearest <= 1.5) score += 120;
    else if (nearest <= 3) score += 60;
  }
  return score;
}

function recordStatusEvents(status) {
  const nowText = fmtShortDate(new Date().toISOString());
  const prevBySymbol = new Map(((previousStatus || {}).symbols || []).map((stock) => [stock.symbol, stock]));
  const add = (kind, text) => {
    const key = kind + "|" + text;
    if (eventStream[0] && eventStream[0].key === key) return;
    eventStream.unshift({ key, kind, text, at: nowText });
  };
  for (const stock of activeSymbols(status)) {
    const prev = prevBySymbol.get(stock.symbol) || {};
    if (stock.last_error && stock.last_error !== prev.last_error) add("danger", stock.symbol + " 异常：" + stock.last_error);
    if (stock.pending_order_id && stock.pending_order_id !== prev.pending_order_id) add("pending", stock.symbol + " 新挂单：" + actionText(stock.pending_side) + " @ " + fmtNumber(stock.pending_price));
    if (!stock.pending_order_id && prev.pending_order_id) add("ok", stock.symbol + " 挂单已结束：" + prev.pending_order_id);
    if (stock.decision && stock.decision !== prev.decision && (stock.buy_trail_armed || stock.sell_trail_armed || stock.pending_order_id || stock.last_error)) {
      add("info", stock.symbol + " " + stock.decision);
    }
  }
  const prevToken = (previousStatus || {}).access_token || {};
  const token = status.access_token || {};
  if (token.last_refresh_at && token.last_refresh_at !== prevToken.last_refresh_at) add("ok", "访问令牌已刷新：" + fmtDate(token.last_refresh_at));
  if (status.config_reloaded_at && status.config_reloaded_at !== (previousStatus || {}).config_reloaded_at) add("info", "配置已热加载：" + fmtDate(status.config_reloaded_at));
  eventStream = eventStream.slice(0, 20);
  previousStatus = status;
}

function pill(text, cls) {
  return '<span class="pill ' + esc(cls || "") + '">' + text + '</span>';
}

function renderTop(status) {
  const market = status.market || {};
  const token = status.access_token || {};
  const mode = document.getElementById("mode-pill");
  mode.textContent = status.dry_run ? "演练模式" : "真实下单";
  mode.className = "pill " + (status.dry_run ? "warn" : "danger");

  const lastUpdated = status.last_updated_at ? new Date(status.last_updated_at) : null;
  const staleThreshold = market.open ? 15000 : 16 * 60 * 1000;
  const stale = !lastUpdated || Number.isNaN(lastUpdated.getTime()) || (Date.now() - lastUpdated) > staleThreshold;
  let pollText = "数据正常", pollClass = "ok";
  if (pollFailureCount > 0) { pollText = "接口异常 ×" + pollFailureCount; pollClass = "danger"; }
  else if (!market.open) { pollText = "低频轮询"; pollClass = ""; }
  else if (stale) { pollText = "数据可能过期"; pollClass = "warn"; }

  let tokenText = "令牌正常", tokenClass = "ok";
  if (!token.has_token) { tokenText = "令牌缺失"; tokenClass = "danger"; }
  else if (token.last_refresh_state === "error" || token.last_refresh_state === "missing") { tokenText = "令牌刷新异常"; tokenClass = "danger"; }
  else if (token.refresh_due_now) { tokenText = "令牌刷新中"; tokenClass = "warn"; }
  const tokenTitle = token.last_refresh_message && tokenClass === "danger" ? token.last_refresh_message : "到期 " + fmtDate(token.expires_at);

  document.getElementById("top-meta").innerHTML =
    pill(esc(marketPhaseText(market.current_phase)) + (market.trading_day ? "" : " · 非交易日"), market.open ? "ok" : "") +
    '<span class="pill ' + tokenClass + '" title="' + esc(tokenTitle) + '">' + tokenText + '</span>' +
    pill(pollText, pollClass) +
    '<span class="muted">刷新 ' + fmtTime(status.last_updated_at) + '</span>';
}

function renderKPIs(status) {
  const exposure = status.exposure || {};
  const usd = (status.account_balances || []).find((account) => account.currency === "USD") || (status.account_balances || [])[0] || {};
  const cash = (usd.cash_infos || [])[0] || {};
  const used = toNumber(exposure.used);
  const limit = toNumber(exposure.limit);
  const ratio = used !== null && limit ? Math.min(100, Math.max(0, (used / limit) * 100)) : null;
  let exposureValue = "未启用", exposureClass = "warn";
  if (exposure.enabled) {
    exposureClass = exposure.reached ? "danger" : "";
    exposureValue = limit !== null ? fmtNumber(used ?? 0, 0) + ' <span class="kpi-of">/ ' + fmtNumber(limit, 0) + '</span>' : "已启用";
  }
  const bar = ratio === null ? "" : '<div class="bar"><i class="' + exposureClass + '" style="width:' + ratio.toFixed(1) + '%"></i></div>';
  const kpi = (label, value, cls, extra) =>
    '<div class="kpi"><div class="kpi-label">' + label + '</div><div class="kpi-value ' + (cls || "") + '">' + value + '</div>' + (extra || "") + '</div>';
  const margins = activeSymbols(status).filter((stock) => stock.use_margin).length;
  const accountNote = status.account_error ? '<div class="kpi-note danger">' + esc(status.account_error) + '</div>' : "";
  document.getElementById("kpis").innerHTML =
    kpi("已投入 / 上限", exposureValue, exposureClass, bar) +
    kpi("可用现金", fmtNumber(cash.available_cash, 0), "", accountNote) +
    kpi("购买力", fmtNumber(usd.buy_power, 0)) +
    kpi("剩余融资", fmtNumber(usd.remaining_finance_amount, 0), "", '<div class="kpi-note">融资标的 ' + margins + ' 个</div>') +
    kpi("净资产", fmtNumber(usd.net_assets, 0), "", usd.risk_level ? '<div class="kpi-note">风险等级 ' + esc(usd.risk_level) + '</div>' : "");
}

function updateToolbar(status) {
  const symbols = activeSymbols(status);
  const limited = limitedSymbols(status);
  const counts = {
    all: symbols.length,
    pending: symbols.filter((stock) => stock.pending_order_id).length,
    limited: symbols.filter((stock) => limited.has(stock.symbol)).length,
    error: symbols.filter((stock) => stock.last_error).length
  };
  for (const [key, value] of Object.entries(counts)) {
    const el = document.getElementById("count-" + key);
    if (el) el.textContent = value;
  }
  document.querySelectorAll("#stock-filter .segment").forEach((button) => {
    button.classList.toggle("active", button.dataset.filter === stockFilter);
  });
}

function kv(label, value, cls) {
  return '<div class="kv' + (cls ? " " + cls : "") + '"><dt>' + label + '</dt><dd>' + value + '</dd></div>';
}

function trailText(stock, view, buy) {
  const pct = buy ? stock.buy_trail_percent : stock.sell_trail_percent;
  const extreme = buy ? stock.buy_trail_lowest : stock.sell_trail_highest;
  const word = buy ? "反弹" : "回撤";
  if (!view.armed) return '<span class="muted">未激活' + (pct ? " · " + word + " " + esc(pct) + "%" : "") + '</span>';
  return (buy ? "最低 " : "最高 ") + fmtNumber(extreme) + " → " + word + " " + esc(pct) + "% = " + fmtNumber(view.trail);
}

function tagList(tags, limit) {
  return (tags || []).map((tag) => '<span class="tag ' + esc(tag.kind) + (limit ? " limit" : "") + '">' + esc(tag.label) + '</span>').join("");
}

function renderSide(stock, view, buy, limited) {
  const title = buy ? "买入" : "卖出";
  const dist = view.delta === null ? "" :
    '<span class="dist ' + view.tone + '">' + fmtSigned(view.delta) + ' <small>' + fmtPct(view.deltaPct) + '</small></span>';
  const rows = buy
    ? kv("基础价", fmtNumber(view.base) + (stock.next_buy_percent ? ' <span class="muted">档位 -' + esc(stock.next_buy_percent) + '% · 参考 ' + fmtNumber(stock.next_buy_ref) + '</span>' : "")) +
      kv("追踪", trailText(stock, view, true)) +
      kv("资金", fmtNumber(stock.next_buy_cash_needed) + (toNumber(stock.next_buy_margin_use) ? ' <span class="muted">含融资 ' + fmtNumber(stock.next_buy_margin_use) + '</span>' : "")) +
      kv("还能买", esc(stock.remaining_buy_lots ?? 0) + " 笔", limited.has(stock.symbol) ? "warn" : "")
    : kv("基础价", fmtNumber(view.base) + ' <span class="muted">参考 ' + fmtNumber(stock.next_sell_ref) + '</span>') +
      kv("追踪", trailText(stock, view, false)) +
      kv("预估毛利", fmtNumber(stock.next_sell_profit));
  const reason = buy ? stock.next_buy_reason : stock.next_sell_reason;
  const tags = buy ? tagList(stock.buy_capacity_limit_tags, true) + tagList(stock.buy_capacity_detail_tags, false) : "";
  return '<section class="side ' + (buy ? "buy" : "sell") + (view.armed ? " armed" : "") + '">' +
    '<div class="side-head"><span class="side-title">' + title + (view.armed ? ' <b>追踪中</b>' : "") + '</span>' + dist + '</div>' +
    '<div class="side-price">' + fmtNumber(view.effective) + ' <small>' + esc(view.source) + '</small></div>' +
    (reason ? '<div class="side-reason">' + esc(reason) + '</div>' : "") +
    '<dl class="kvs">' + rows + '</dl>' +
    (buy && limited.has(stock.symbol) && stock.buy_capacity_reason ? '<div class="side-reason warn">' + esc(stock.buy_capacity_reason) + '</div>' : "") +
    (tags ? '<div class="tags">' + tags + '</div>' : "") +
    '</section>';
}

function renderStockCard(stock, state, limited) {
  const trend = stock.price_trend || "flat";
  const trendIcon = trend === "up" ? "▲" : trend === "down" ? "▼" : "";
  const change = stock.price_change ? fmtSigned(toNumber(stock.price_change)) + (stock.price_change_pct ? " (" + fmtSigned(toNumber(stock.price_change_pct)) + "%)" : "") : "";
  const detailRows =
    kv("最近动作", esc(actionText(stock.last_action)) + (stock.last_filled_at_text ? ' <span class="muted">' + esc(stock.last_filled_at_text) + (stock.last_filled_price ? " @ " + fmtNumber(stock.last_filled_price) : "") + '</span>' : "")) +
    kv("最近买 / 卖", fmtNumber(stock.last_buy_price) + " / " + fmtNumber(stock.last_sell_price)) +
    kv("最近订单", esc(stock.last_order_status || "-") + (stock.last_order_msg ? ' <span class="muted">' + esc(stock.last_order_msg) + '</span>' : "")) +
    (stock.pending_order_id ? kv("挂单号", '<code>' + esc(stock.pending_order_id) + '</code>') : "") +
    (stock.remark ? kv("备注", esc(stock.remark)) : "");
  return '<article class="stock ' + esc(state.kind) + '">' +
    '<header class="stock-head">' +
      '<div class="stock-id"><h3>' + esc(stock.symbol) + '</h3><span class="badge ' + esc(state.kind) + '">' + esc(state.label) + '</span></div>' +
      '<div class="stock-quote"><div class="price ' + esc(trend) + '">' + fmtNumber(stock.price) + '</div>' +
        (change ? '<div class="change ' + esc(trend) + '">' + trendIcon + ' ' + change + '</div>' : "") + '</div>' +
    '</header>' +
    '<p class="stock-next ' + esc(state.kind) + '">' + esc(state.text) + '</p>' +
    '<div class="sides">' + renderSide(stock, state.buy, true, limited) + renderSide(stock, state.sell, false, limited) + '</div>' +
    '<div class="stock-pos">' +
      '<span>持仓 <b>' + esc(stock.position_qty ?? 0) + '</b> 股' + (stock.available_qty !== stock.position_qty ? ' <span class="muted">可卖 ' + esc(stock.available_qty ?? 0) + '</span>' : "") + '</span>' +
      '<span>成本 <b>' + fmtNumber(stock.cost_price) + '</b></span>' +
      '<span>市值 <b>' + fmtNumber(stock.position_notional, 0) + '</b> <span class="muted">/ 满仓 ' + fmtNumber(stock.max_position_notional, 0) + ' (' + esc(stock.max_lots || 0) + '×' + esc(stock.order_quantity || 0) + ')</span></span>' +
      '<span class="muted">' + (stock.use_margin ? "可融资" : "仅现金") + '</span>' +
    '</div>' +
    '<details class="stock-more" data-symbol="' + esc(stock.symbol) + '"><summary>详情</summary>' +
      '<dl class="kvs">' + detailRows + '</dl>' +
      '<div class="decision">' + esc(stock.decision || "-") + '</div>' +
    '</details>' +
  '</article>';
}

function renderStocks(status) {
  const grid = document.getElementById("stock-grid");
  const openDetails = new Set(
    Array.from(grid.querySelectorAll("details.stock-more[open]")).map((el) => el.dataset.symbol).filter(Boolean)
  );
  const limited = limitedSymbols(status);
  const items = activeSymbols(status)
    .filter((stock) => stockMatchesFilter(stock, limited))
    .map((stock) => ({ stock, state: stockState(stock, limited) }));
  items.sort((a, b) => {
    const diff = urgencyScore(b.stock, b.state) - urgencyScore(a.stock, a.state);
    return diff !== 0 ? diff : String(a.stock.symbol).localeCompare(String(b.stock.symbol));
  });
  grid.innerHTML = items.length
    ? items.map(({ stock, state }) => renderStockCard(stock, state, limited)).join("")
    : '<div class="empty">当前筛选条件下暂无标的</div>';
  if (openDetails.size) {
    grid.querySelectorAll("details.stock-more").forEach((el) => {
      if (openDetails.has(el.dataset.symbol)) el.open = true;
    });
  }
}

function renderDisabledStocks(status) {
  const disabled = disabledSymbols(status);
  const section = document.getElementById("disabled-section");
  section.innerHTML = disabled.length
    ? '<span class="muted">已禁用</span>' + disabled.map((stock) =>
        '<span class="disabled-item" title="' + esc(stock.remark || "") + '">' + esc(stock.symbol) + '</span>').join("")
    : "";
}

function renderAccount(status) {
  const section = document.getElementById("account-section");
  if (status.account_error) {
    section.innerHTML = '<div class="danger">' + esc(status.account_error) + '</div>';
    return;
  }
  const accounts = status.account_balances || [];
  if (!accounts.length) {
    section.innerHTML = '<div class="muted">暂无账户资产数据</div>';
    return;
  }
  const cols = ["币种", "总现金", "可用现金", "可取现金", "冻结现金", "在途结算", "最大融资", "剩余融资", "购买力", "净资产", "风险等级"];
  section.innerHTML =
    '<table><thead><tr>' + cols.map((c) => "<th>" + c + "</th>").join("") + '</tr></thead><tbody>' +
    accounts.map((account) => {
      const cash = (account.cash_infos || [])[0] || {};
      const cells = [
        "<strong>" + esc(account.currency || "-") + "</strong>",
        fmtNumber(account.total_cash), fmtNumber(cash.available_cash), fmtNumber(cash.withdraw_cash),
        fmtNumber(cash.frozen_cash), fmtNumber(cash.settling_cash), fmtNumber(account.max_finance_amount),
        fmtNumber(account.remaining_finance_amount), fmtNumber(account.buy_power), fmtNumber(account.net_assets),
        esc(account.risk_level || "-")
      ];
      return "<tr>" + cells.map((cell, i) => '<td data-label="' + cols[i] + '">' + cell + "</td>").join("") + "</tr>";
    }).join("") +
    '</tbody></table>';
}

function renderEventStream() {
  const section = document.getElementById("event-section");
  if (!eventStream.length) {
    section.innerHTML = "";
    return;
  }
  const wasOpen = !!section.querySelector("details[open]");
  section.innerHTML =
    '<details class="card fold"' + (wasOpen ? " open" : "") + '><summary>最近事件 <span class="muted">' + eventStream.length + '</span></summary>' +
    '<div class="events">' +
      eventStream.map((event) =>
        '<div class="event ' + esc(event.kind) + '"><span class="muted">' + esc(event.at) + '</span><span>' + esc(event.text) + '</span></div>'
      ).join("") +
    '</div></details>';
}

function renderFoot(status) {
  document.getElementById("foot").innerHTML =
    '<span>配置 <code>' + esc(status.config_path || "-") + '</code></span>' +
    '<span>' + (status.config_reloaded_at ? "热加载 " + fmtDate(status.config_reloaded_at) : "启动后未热加载") + '</span>' +
    '<span>启动 ' + fmtDate(status.started_at) + '</span>';
}

function renderStatus(status) {
  recordStatusEvents(status);
  renderTop(status);
  renderKPIs(status);
  updateToolbar(status);
  renderStocks(status);
  renderDisabledStocks(status);
  renderAccount(status);
  renderEventStream();
  renderFoot(status);
}

function bindToolbar() {
  document.querySelectorAll("#stock-filter .segment").forEach((button) => {
    button.addEventListener("click", () => {
      stockFilter = button.dataset.filter || "all";
      renderStatus(latestStatus);
    });
  });
}

async function pollStatus() {
  try {
    const resp = await fetch(buildStatusURL(), { cache: "no-cache" });
    if (!resp.ok) {
      pollFailureCount += 1;
      renderTop(latestStatus);
      return;
    }
    const status = await resp.json();
    pollFailureCount = 0;
    latestStatus = status;
    renderStatus(status);
  } catch (_) {
    pollFailureCount += 1;
    renderTop(latestStatus);
  }
}

function nextPollDelay() {
  return ((latestStatus || {}).market || {}).open ? openPollIntervalMs : closedPollIntervalMs;
}

function scheduleNextPoll() {
  setTimeout(async () => {
    await pollStatus();
    scheduleNextPoll();
  }, nextPollDelay());
}

bindToolbar();
renderStatus(initialStatus);
scheduleNextPoll();
