(() => {
  const DURATION_UNITS = [["ms", 1], ["s", 1000], ["m", 60000], ["h", 3600000]];
  const EXPOSURE_PRESETS = [["关闭上限", "0"], ["5,000", "5000"], ["10,000", "10000"], ["20,000", "20000"], ["50,000", "50000"]];
  const NEW_STOCK = {
    symbol: "", initial_price: "", sell_percent: "5", trail_percent: "1.5", buy_percent: "3", min_profit: "10",
    use_margin: false, max_lots: 3, order_quantity: 1, remark: "", enabled: false,
  };
  const LABELS = {
    "engine.dry_run": "运行模式", "engine.max_exposure": "最大投入金额", "engine.poll_interval": "轮询周期", "engine.order_timeout": "挂单超时",
    "engine.state_file": "状态文件", "engine.access_token_auto_refresh": "自动刷新令牌", "engine.access_token_refresh_before": "提前刷新时间",
    "engine.access_token_env_file": "凭证文件", "engine.trade_api_window": "限频窗口", "engine.trade_api_max_calls": "窗口内最大调用",
    "engine.trade_api_min_gap": "调用最小间隔", "engine.quote_request_timeout": "请求超时",
    "server.host": "监听地址", "server.port": "监听端口", "server.auth_secret": "面板密钥",
    "fees.commission_per_share": "佣金 / 股", "fees.commission_min": "佣金每单最低", "fees.platform_per_share": "平台费 / 股", "fees.platform_min": "平台费每单最低",
    "fees.settlement_per_share": "交收费 / 股", "fees.settlement_min": "交收费每单最低", "fees.sec_rate": "SEC 规费比例", "fees.finra_per_share": "FINRA 费 / 股",
    symbol: "代码", initial_price: "首次建仓价", sell_percent: "卖出阈值", trail_percent: "追踪幅度", buy_percent: "补仓阈值", min_profit: "最低盈利",
    use_margin: "允许融资", max_lots: "最大笔数", order_quantity: "每笔数量", remark: "订单备注", enabled: "启用",
  };
  const FEE_PRESETS = {
    longbridge: { commission_per_share: "0", commission_min: "0", platform_per_share: "0.005", platform_min: "1", settlement_per_share: "0.003", settlement_min: "0.01", sec_rate: "0.0000051", finra_per_share: "0.000166" },
    none: { commission_per_share: "0", commission_min: "0", platform_per_share: "0", platform_min: "0", settlement_per_share: "0", settlement_min: "0", sec_rate: "0", finra_per_share: "0" },
  };
  const STRATEGY_KEYS = ["initial_price", "sell_percent", "trail_percent", "buy_percent", "min_profit", "max_lots", "order_quantity"];

  const state = { open: false, loading: false, error: "", path: "", original: null, draft: null, lotSizes: {}, saving: false };
  let drawer = null;
  let backdrop = null;

  function apiURL(name) {
    const path = window.location.pathname || "/";
    const normalized = path.endsWith("/") ? path : path.substring(0, path.lastIndexOf("/") + 1);
    return new URL("api/" + name, window.location.origin + normalized).toString();
  }
  const clone = (value) => JSON.parse(JSON.stringify(value));
  const num = (value) => { const n = toNumber(value); return n === null ? 0 : n; };
  const money = (value) => "$" + fmtNumber(value, 0);
  const pct = (value) => fmtNumber(value, 2).replace(/\.?0+$/, "") + "%";
  function fib(n) { let a = 1, b = 1; for (let i = 3; i <= n; i++) [a, b] = [b, a + b]; return n <= 0 ? 0 : b; }
  function getPath(obj, path) { return path.split(".").reduce((acc, key) => (acc == null ? undefined : acc[key]), obj); }
  function setPath(obj, path, value) {
    const keys = path.split(".");
    const last = keys.pop();
    const target = keys.reduce((acc, key) => acc[key], obj);
    target[last] = value;
  }
  function parseDuration(text) {
    const re = /(\d+(?:\.\d+)?)(ms|s|m|h)/g;
    let total = 0, matched = false, m;
    while ((m = re.exec(String(text || "")))) { matched = true; total += parseFloat(m[1]) * DURATION_UNITS.find((u) => u[0] === m[2])[1]; }
    return matched ? total : null;
  }
  function durationParts(text) {
    const ms = parseDuration(text);
    if (ms === null) return { n: "", unit: "s" };
    for (let i = DURATION_UNITS.length - 1; i >= 0; i--) {
      if (ms % DURATION_UNITS[i][1] === 0) return { n: ms / DURATION_UNITS[i][1], unit: DURATION_UNITS[i][0] };
    }
    return { n: ms, unit: "ms" };
  }
  function fmtDuration(text) {
    const ms = parseDuration(text);
    if (ms === null) return esc(text);
    if (ms >= 3600000 && ms % 3600000 === 0) return ms / 3600000 + " 小时";
    if (ms >= 60000 && ms % 60000 === 0) return ms / 60000 + " 分钟";
    if (ms >= 1000) return (ms / 1000).toString().replace(/\.0+$/, "") + " 秒";
    return ms + " 毫秒";
  }
  function statusFor(symbol) {
    return ((latestStatus || {}).symbols || []).find((item) => item.symbol === symbol) || null;
  }
  function normalizeSymbol(value) {
    let sym = String(value || "").trim().toUpperCase().replace(/\s+/g, "");
    if (sym && !sym.includes(".")) sym += ".US";
    return sym;
  }
  function autoRemark(symbol) {
    const base = normalizeSymbol(symbol).replace(/\.US$/, "").toLowerCase().replace(/[^a-z0-9]+/g, "");
    return base ? "snap-" + base : "";
  }

  // ---------- fees ----------
  function estimateFee(fees, side, price, qty) {
    if (!(qty > 0)) return 0;
    const item = (per, min) => Math.max(num(per) * qty, num(min));
    let total = item(fees.commission_per_share, fees.commission_min) + item(fees.platform_per_share, fees.platform_min) + item(fees.settlement_per_share, fees.settlement_min);
    if (side === "sell") {
      if (num(fees.sec_rate) > 0) total += Math.max(price * qty * num(fees.sec_rate), 0.01);
      if (num(fees.finra_per_share) > 0) total += Math.min(num(fees.finra_per_share) * qty, 8.3);
    }
    return total;
  }
  function roundTripFee(fees, buyPrice, sellPrice, qty) {
    return estimateFee(fees, "buy", buyPrice, qty) + estimateFee(fees, "sell", sellPrice, qty);
  }

  // ---------- derived numbers ----------
  function stockDerived(stock) {
    const st = statusFor(normalizeSymbol(stock.symbol)) || {};
    const price = toNumber(st.price) ?? num(stock.initial_price);
    const qty = Math.max(0, parseInt(stock.order_quantity, 10) || 0);
    const lots = Math.max(0, parseInt(stock.max_lots, 10) || 0);
    const sell = num(stock.sell_percent), trail = num(stock.trail_percent), buy = num(stock.buy_percent), minProfit = num(stock.min_profit);
    const perLot = price * qty;
    const grossAtSell = perLot * sell / 100;
    const fee = roundTripFee(state.draft.fees || {}, price, price * (1 + sell / 100), qty);
    const netAtSell = grossAtSell - fee;
    const feeRatio = grossAtSell > 0 ? fee / grossAtSell : (fee > 0 ? Infinity : 0);
    const effectiveSell = perLot > 0 ? Math.max(sell, (minProfit + fee) / perLot * 100) : sell;
    const locked = ((1 + sell / 100) * (1 - trail / 100) - 1) * 100;
    const ladder = [];
    for (let n = 1; n < lots; n++) ladder.push(buy * fib(n + 2) / 2);
    const heldLots = qty > 0 ? Math.ceil((st.position_qty || 0) / qty) : 0;
    return { st, price, livePrice: toNumber(st.price), qty, lots, sell, trail, buy, minProfit, perLot, full: perLot * lots, grossAtSell, fee, netAtSell, feeRatio, effectiveSell, locked, ladder, heldLots };
  }
  function immediateBuy(stock, d, before) {
    if (stock.enabled === false || d.livePrice === null || (d.st.position_qty || 0) > 0 || d.st.pending_order_id) return false;
    if (!(d.livePrice <= num(stock.initial_price))) return false;
    return !before || before.enabled === false || STRATEGY_KEYS.some((key) => String(stock[key]) !== String(before[key]));
  }
  function heldText(position, pending) {
    const parts = [];
    if (position > 0) parts.push(" " + position + " 股持仓");
    if (pending) parts.push("未完成挂单");
    return parts.join("和");
  }
  function enabledStocks(cfg) { return (cfg.stocks || []).filter((s) => s.enabled !== false); }
  function totalFull(cfg) { return enabledStocks(cfg).reduce((sum, s) => sum + stockDerived(s).full, 0); }

  // ---------- analysis ----------
  function analyze() {
    const draft = state.draft, orig = state.original;
    const errors = [], issues = [], changes = [];
    const live = !draft.engine.dry_run;
    const switchingLive = orig.engine.dry_run && live;
    const push = (level, text) => issues.push({ level, text });
    const change = (label, from, to, kind) => changes.push({ label, from, to, kind: kind || "changed" });
    const fmtVal = (key, value) => {
      if (key === "engine.dry_run") return value ? "演练模式" : "真实下单";
      if (typeof value === "boolean") return value ? "开" : "关";
      if (key === "server.auth_secret") return value ? "••••••" : "(空)";
      if (/interval|timeout|window|gap|before/.test(key)) return fmtDuration(value);
      if (key === "engine.max_exposure") return num(value) > 0 ? money(value) : "关闭";
      return String(value ?? "");
    };

    for (const key of Object.keys(draft.fees || {})) if (num(draft.fees[key]) < 0) errors.push((LABELS["fees." + key] || key) + " 不能为负");
    for (const section of ["engine", "server", "fees"]) {
      for (const key of Object.keys(draft[section])) {
        const path = section + "." + key;
        if (String(draft[section][key]) !== String(orig[section][key])) change(LABELS[path] || path, fmtVal(path, orig[section][key]), fmtVal(path, draft[section][key]));
      }
    }
    if (switchingLive) push("danger", "从演练模式切换到真实下单：保存后引擎会立即按策略向券商提交真实订单。");
    const exposure = num(draft.engine.max_exposure);
    if (live && exposure <= 0) push("danger", "未设置最大投入金额：真实下单时没有总资金上限，补仓会一直进行到现金或融资耗尽。");
    const full = totalFull(draft);
    if (exposure > 0 && full > exposure) push("info", "全部启用标的满仓合计约 " + money(full) + "，超过上限 " + money(exposure) + "；到达上限后将停止买入，这是预期保护。");
    const poll = parseDuration(draft.engine.poll_interval);
    if (poll === null || poll < 1000) errors.push("轮询周期至少 1 秒");
    else if (poll < 3000) push("warn", "轮询周期低于 3 秒，容易触发券商接口限频。");
    const timeout = parseDuration(draft.engine.order_timeout);
    if (timeout === null || timeout < 10000) errors.push("挂单超时至少 10 秒");
    if (parseDuration(draft.engine.access_token_refresh_before) === null) errors.push("提前刷新时间格式无效");
    for (const key of ["trade_api_window", "trade_api_min_gap", "quote_request_timeout"]) if (parseDuration(draft.engine[key]) === null) errors.push(LABELS["engine." + key] + " 格式无效");
    if (!(parseInt(draft.engine.trade_api_max_calls, 10) > 0)) errors.push("窗口内最大调用次数必须大于 0");
    if (num(draft.engine.max_exposure) < 0) errors.push("最大投入金额不能为负");
    if (draft.engine.state_file !== orig.engine.state_file) push("info", "状态文件路径在运行中不会切换，重启后才生效。");
    if (draft.server.host !== orig.server.host || String(draft.server.port) !== String(orig.server.port)) push("info", "监听地址 / 端口需要重启进程才生效。");
    if (draft.server.auth_secret !== orig.server.auth_secret) push("danger", "面板密钥保存后立即生效：反向代理注入的密钥必须同步更新，否则面板会立刻返回 401。");
    const port = parseInt(draft.server.port, 10);
    if (!(port > 0 && port < 65536)) errors.push("监听端口需在 1-65535 之间");
    if (!String(draft.server.host || "").trim()) errors.push("监听地址不能为空");

    const seenSymbols = new Map(), seenRemarks = new Map();
    const origBySymbol = new Map((orig.stocks || []).map((s) => [normalizeSymbol(s.symbol), s]));
    const draftSymbols = new Set();
    draft.stocks.forEach((stock, i) => {
      const sym = normalizeSymbol(stock.symbol);
      const name = sym || ("第 " + (i + 1) + " 个标的");
      const d = stockDerived(stock);
      const enabled = stock.enabled !== false;
      const before = origBySymbol.get(sym);
      draftSymbols.add(sym);
      if (!/^[A-Z0-9][A-Z0-9.\-]*\.US$/.test(sym)) errors.push(name + ": 代码需为美股代码，形如 AAPL.US");
      if (seenSymbols.has(sym)) errors.push(name + ": 代码重复"); else seenSymbols.set(sym, i);
      if (!(num(stock.initial_price) > 0)) errors.push(name + ": 首次建仓价必须大于 0");
      if (!(d.sell > 0 && d.sell < 100)) errors.push(name + ": 卖出阈值需在 0-100% 之间");
      if (!(d.buy > 0 && d.buy < 100)) errors.push(name + ": 补仓阈值需在 0-100% 之间");
      if (!(d.trail > 0 && d.trail < 100)) errors.push(name + ": 追踪幅度需在 0-100% 之间");
      else if (d.trail >= d.sell) errors.push(name + ": 追踪幅度必须小于卖出阈值，否则回撤后可能低于买入价卖出");
      if (d.minProfit < 0) errors.push(name + ": 最低盈利不能为负");
      if (!(d.lots >= 1 && d.lots <= 100)) errors.push(name + ": 最大笔数需在 1-100 之间");
      if (!(d.qty >= 1)) errors.push(name + ": 每笔数量至少 1 股");
      const lot = state.lotSizes[sym] || 0;
      if (lot > 0 && d.qty % lot !== 0) errors.push(name + ": 每笔数量必须是每手 " + lot + " 股的整数倍");
      if (enabled) {
        const remark = String(stock.remark || "").trim();
        if (!remark) errors.push(name + ": 启用的标的必须填写订单备注");
        else if (seenRemarks.has(remark)) errors.push(name + ": 订单备注与 " + seenRemarks.get(remark) + " 重复"); else seenRemarks.set(remark, name);
      }
      const position = d.st.position_qty || 0;
      if (!before) {
        change(name, "", enabled ? "新增并启用" : "新增（未启用）", "added");
      } else {
        for (const key of Object.keys(stock)) {
          if (String(stock[key]) !== String(before[key] ?? (key === "enabled" ? true : key === "trail_percent" ? "1" : ""))) {
            change(name + " · " + (LABELS[key] || key), fmtVal(key, before[key]), fmtVal(key, stock[key]));
          }
        }
      }
      const strategyChanged = before && STRATEGY_KEYS.some((key) => String(stock[key]) !== String(before[key]));
      if (immediateBuy(stock, d, before)) {
        push(live ? "danger" : "warn", name + ": 现价 " + fmtNumber(d.livePrice) + " 已低于首次建仓价 " + fmtNumber(stock.initial_price) + "，空仓状态下，待反弹 " + pct(d.trail) + " 后就会买入一笔，约 " + money(d.perLot) + "。");
      }
      if (strategyChanged && d.st.pending_order_id) push("warn", name + ": 策略参数变化会撤销当前挂单 " + d.st.pending_order_id + " 并重置追踪状态。");
      else if (strategyChanged && (d.st.buy_trail_armed || d.st.sell_trail_armed)) push("info", name + ": 策略参数变化会重置正在进行的追踪状态。");
      if (before && String(stock.order_quantity) !== String(before.order_quantity) && position > 0) push("warn", name + ": 修改每笔数量后，现有 " + position + " 股会按新数量折算为 " + d.heldLots + " 笔，影响补仓阶梯与卖出判断。");
      if (before && before.enabled !== false && !enabled && (position > 0 || d.st.pending_order_id)) push("danger", name + ": 停用后程序不再管理其" + heldText(position, d.st.pending_order_id) + "，需要手动处理。");
      if (stock.use_margin && (!before || !before.use_margin)) push("warn", name + ": 开启融资买入会产生利息并放大亏损。");
      if (enabled && d.perLot > 0 && d.effectiveSell > d.sell + 0.0001) push("info", name + ": 最低盈利 " + money(d.minProfit) + " 高于到卖点的单笔净利 " + fmtNumber(d.netAtSell) + "，实际需上涨约 " + pct(d.effectiveSell) + " 才会卖出。");
      if (enabled && d.grossAtSell > 0 && d.feeRatio >= 0.5) push("danger", name + ": 一买一卖费用约 " + fmtNumber(d.fee) + "，占到卖点毛利 " + fmtNumber(d.grossAtSell) + " 的 " + pct(d.feeRatio * 100) + "，大半利润付给了券商；请提高卖出阈值或加大每笔数量。");
      else if (enabled && d.grossAtSell > 0 && d.feeRatio >= 0.2) push("warn", name + ": 一买一卖费用约 " + fmtNumber(d.fee) + "，占到卖点毛利的 " + pct(d.feeRatio * 100) + "，每单最低收费对小单影响明显。");
      if (enabled && d.buy > 0 && d.buy < 1) push("warn", name + ": 补仓阈值低于 1%，会频繁加仓迅速打满仓位。");
      if (enabled && d.lots > 0 && d.heldLots > d.lots) push("info", name + ": 当前已持有约 " + d.heldLots + " 笔，超过最大笔数 " + d.lots + "，将不再补仓直到卖出。");
    });
    for (const [sym, before] of origBySymbol) {
      if (draftSymbols.has(sym)) continue;
      const st = statusFor(sym) || {};
      change(sym, before.enabled === false ? "未启用" : "启用", "移除", "removed");
      if ((st.position_qty || 0) > 0 || st.pending_order_id) push("danger", sym + ": 移除后程序不再管理其" + heldText(st.position_qty || 0, st.pending_order_id) + "，需要手动处理。");
    }
    if (!draft.stocks.length) errors.push("至少需要一个标的");
    const dangers = issues.filter((i) => i.level === "danger").length;
    const warns = issues.filter((i) => i.level === "warn").length;
    return { errors, issues, changes, switchingLive, dangers, warns, live };
  }

  // ---------- rendering ----------
  function fieldWrap(path, label, control, help, opts) {
    opts = opts || {};
    return '<div class="field' + (opts.wide ? " wide" : "") + '" data-field="' + esc(path) + '">' +
      '<div class="field-label"><span>' + esc(label) + '</span>' + (opts.note ? "<small>" + esc(opts.note) + "</small>" : "") + "</div>" +
      control +
      (help ? '<div class="field-help">' + help + "</div>" : "") +
      '<div class="field-hint" data-hint="' + esc(path) + '"></div></div>';
  }
  function numberInput(path, value, opts) {
    opts = opts || {};
    return '<div class="control"><input type="number" inputmode="decimal" data-path="' + esc(path) + '" data-type="' + (opts.type || "decimal") + '" value="' + esc(value) + '"' +
      (opts.step ? ' step="' + opts.step + '"' : "") + (opts.min !== undefined ? ' min="' + opts.min + '"' : "") + (opts.max !== undefined ? ' max="' + opts.max + '"' : "") +
      (opts.placeholder ? ' placeholder="' + esc(opts.placeholder) + '"' : "") + ">" +
      (opts.unit ? '<span class="unit">' + esc(opts.unit) + "</span>" : "") + "</div>";
  }
  function percentField(path, label, value, help, range) {
    return fieldWrap(path, label,
      numberInput(path, value, { unit: "%", step: "0.1", min: 0 }) +
      '<input type="range" class="range" data-range-for="' + esc(path) + '" min="' + range[0] + '" max="' + range[1] + '" step="' + range[2] + '" value="' + esc(num(value)) + '">',
      help);
  }
  function stepperField(path, label, value, help, step, min, note) {
    return fieldWrap(path, label,
      '<div class="control"><button type="button" class="step" data-action="step" data-step="-1" data-path="' + esc(path) + '">−</button>' +
      '<input type="number" class="stepper-input" inputmode="numeric" data-path="' + esc(path) + '" data-type="int" data-step-size="' + step + '" data-min="' + min + '" value="' + esc(value) + '">' +
      '<button type="button" class="step" data-action="step" data-step="1" data-path="' + esc(path) + '">+</button></div>',
      help, { note });
  }
  function durationField(path, label, value, help, units) {
    const parts = durationParts(value);
    const options = DURATION_UNITS.filter((u) => !units || units.includes(u[0])).map((u) =>
      '<option value="' + u[0] + '"' + (u[0] === parts.unit ? " selected" : "") + ">" + { ms: "毫秒", s: "秒", m: "分钟", h: "小时" }[u[0]] + "</option>").join("");
    return fieldWrap(path, label,
      '<div class="control"><input type="number" inputmode="numeric" min="0" data-path="' + esc(path) + '" data-type="duration-n" value="' + esc(parts.n) + '">' +
      '<select class="unit" data-path="' + esc(path) + '" data-type="duration-u">' + options + "</select></div>",
      help);
  }
  function textField(path, label, value, help, opts) {
    opts = opts || {};
    return fieldWrap(path, label,
      '<div class="control"><input type="text" data-path="' + esc(path) + '" data-type="text" value="' + esc(value) + '"' + (opts.placeholder ? ' placeholder="' + esc(opts.placeholder) + '"' : "") + ">" + (opts.extra || "") + "</div>",
      help, opts);
  }
  function toggle(path, checked, onText, offText, dangerOn) {
    return '<label class="switch' + (dangerOn ? " danger-on" : "") + '"><input type="checkbox" data-path="' + esc(path) + '" data-type="bool"' + (checked ? " checked" : "") + '><span class="track"></span>' +
      '<span data-toggle-text="' + esc(path) + '" data-on="' + esc(onText) + '" data-off="' + esc(offText) + '">' + esc(checked ? onText : offText) + "</span></label>";
  }
  function toggleField(path, label, checked, help, onText, offText, dangerOn) {
    return fieldWrap(path, label, '<div style="min-height:36px;display:flex;align-items:center">' + toggle(path, checked, onText, offText, dangerOn) + "</div>", help);
  }

  function renderModeCard(cfg) {
    const live = !cfg.engine.dry_run;
    return '<section class="cfg-card' + (live ? " live" : "") + '" data-mode-card>' +
      '<div class="cfg-card-head"><h3>运行模式与资金上限</h3><span class="sub">保存后立即生效</span></div>' +
      '<div class="cfg-grid">' +
      fieldWrap("engine.dry_run", "运行模式",
        '<div class="mode-switch">' +
        '<button type="button" class="mode-option safe' + (live ? "" : " active") + '" data-action="set-mode" data-value="dry"><b>演练模式</b><span>只计算决策、不向券商下单</span></button>' +
        '<button type="button" class="mode-option live' + (live ? " active" : "") + '" data-action="set-mode" data-value="live"><b>真实下单</b><span>按策略真实提交买卖订单</span></button></div>',
        "", { wide: true }) +
      fieldWrap("engine.max_exposure", "最大投入金额",
        numberInput("engine.max_exposure", cfg.engine.max_exposure, { unit: "USD", step: "100", min: 0 }) +
        '<div class="chips">' + EXPOSURE_PRESETS.map(([label, value]) => '<button type="button" class="chip" data-action="chip" data-set-path="engine.max_exposure" data-set-value="' + value + '">' + label + "</button>").join("") + "</div>",
        "现金 + 融资合计的总投入上限，按持仓成本与买入挂单金额统计；下一笔买入会超过该值时停止买入。0 表示不限制。", { wide: true }) +
      "</div></section>";
  }

  function renderStock(stock, i) {
    const p = (key) => "stocks." + i + "." + key;
    const lot = state.lotSizes[normalizeSymbol(stock.symbol)] || 1;
    return '<article class="stock-edit" data-stock-index="' + i + '">' +
      '<div class="stock-edit-head">' +
      '<div class="stock-edit-title"><div class="control"><input type="text" data-path="' + p("symbol") + '" data-type="symbol" value="' + esc(stock.symbol) + '" placeholder="AAPL.US" spellcheck="false"></div></div>' +
      '<div class="stock-edit-actions">' + toggle(p("enabled"), stock.enabled !== false, "已启用", "已停用") +
      '<button type="button" class="btn ghost" data-action="remove-stock" data-index="' + i + '">移除</button></div></div>' +
      '<div class="stock-facts" data-facts="' + i + '"></div>' +
      '<div class="cfg-grid">' +
      fieldWrap(p("initial_price"), "首次建仓价", numberInput(p("initial_price"), stock.initial_price, { unit: "USD", step: "0.01", min: 0 }), "空仓且无历史卖出记录时，现价 ≤ 该价即进入买入流程。") +
      stepperField(p("order_quantity"), "每笔数量", stock.order_quantity, "每次买入 / 卖出的股数。", lot, lot, "每手 " + lot + " 股") +
      percentField(p("sell_percent"), "卖出阈值", stock.sell_percent, "相对买入价上涨多少后进入卖出流程。", [0.5, 30, 0.1]) +
      percentField(p("trail_percent"), "追踪幅度", stock.trail_percent, "卖出：到卖点后从最高价回撤这么多再卖。买入：到买点后从最低价反弹这么多再买。", [0.2, 10, 0.1]) +
      percentField(p("buy_percent"), "补仓阈值", stock.buy_percent, "相对最近买入价下跌多少后补仓；持仓越多，阈值按阶梯放大。", [0.5, 30, 0.1]) +
      stepperField(p("max_lots"), "最大笔数", stock.max_lots, "最多持有多少笔（笔 = 每笔数量）。", 1, 1) +
      fieldWrap(p("min_profit"), "最低盈利", numberInput(p("min_profit"), stock.min_profit, { unit: "USD", step: "1", min: 0 }), "单笔预估毛利低于该值时即使到卖点也不卖。") +
      toggleField(p("use_margin"), "允许融资", !!stock.use_margin, "开启后现金不足时使用融资买入；关闭则仅在现金足够一笔时下单。", "可融资买入", "仅用现金", true) +
      textField(p("remark"), "订单备注", stock.remark, "券商侧用它识别本程序的挂单，启用的标的之间不能重复。", {
        wide: true, placeholder: "snap-aapl",
        extra: '<button type="button" class="icon-btn" data-action="gen-remark" data-index="' + i + '">自动生成</button>',
      }) +
      "</div></article>";
  }

  function renderStocksCard(cfg) {
    return '<section class="cfg-card"><div class="cfg-card-head"><h3>标的 <span class="sub" data-stock-count></span></h3><span class="sub">策略参数变化会重置该标的追踪状态并撤销旧挂单</span></div>' +
      '<div class="stock-list">' + cfg.stocks.map(renderStock).join("") + "</div>" +
      '<div class="cfg-add"><button type="button" class="btn" data-action="add-stock">＋ 添加标的</button></div></section>';
  }

  function renderFeesCard(cfg) {
    const f = cfg.fees || {};
    const fee = (key, label, help, unit) => fieldWrap("fees." + key, label, numberInput("fees." + key, f[key], { unit: unit || "USD", step: "0.001", min: 0 }), help);
    return '<section class="cfg-card"><div class="cfg-card-head"><h3>交易费用</h3><span class="sub">用于把预估盈利和最低盈利换算成扣费后的净利</span></div>' +
      '<div class="chips" style="margin-bottom:12px">' +
      '<button type="button" class="chip" data-action="fee-preset" data-preset="longbridge">长桥美股 · 终身免佣</button>' +
      '<button type="button" class="chip" data-action="fee-preset" data-preset="none">不计费用</button></div>' +
      '<div class="cfg-grid">' +
      fee("commission_per_share", "佣金 / 股", "终身免佣填 0。") + fee("commission_min", "佣金每单最低", "") +
      fee("platform_per_share", "平台费 / 股", "") + fee("platform_min", "平台费每单最低", "小单通常按这个值收。") +
      fee("settlement_per_share", "交收费 / 股", "") + fee("settlement_min", "交收费每单最低", "") +
      fee("sec_rate", "SEC 规费比例", "卖出金额 × 比例，仅卖出。", "×") + fee("finra_per_share", "FINRA 费 / 股", "仅卖出，每单上限 8.30。") +
      "</div><div class=\"field-hint\" data-hint=\"fees\"></div></section>";
  }

  function renderEngineCard(cfg) {
    const e = cfg.engine;
    return '<details class="cfg-card"><summary>引擎参数 <span class="sub">轮询 / 挂单 / 令牌 / 限频</span></summary><div class="cfg-grid">' +
      durationField("engine.poll_interval", "轮询周期", e.poll_interval, "多久拉一次行情、持仓和订单状态。", ["s", "m"]) +
      durationField("engine.order_timeout", "挂单超时", e.order_timeout, "挂单超过该时长仍未成交则自动撤单。", ["s", "m"]) +
      toggleField("engine.access_token_auto_refresh", "自动刷新令牌", e.access_token_auto_refresh !== false, "临期自动调用官方接口刷新 Access Token 并写回凭证文件。", "开启", "关闭") +
      durationField("engine.access_token_refresh_before", "提前刷新时间", e.access_token_refresh_before, "距令牌过期多久时提前刷新。", ["h", "m"]) +
      textField("engine.access_token_env_file", "凭证文件", e.access_token_env_file, "LongPort 凭证 .env 路径，相对配置文件所在目录。") +
      textField("engine.state_file", "状态文件", e.state_file, "本地状态持久化文件；运行中修改不切换，重启后生效。") +
      durationField("engine.trade_api_window", "限频窗口", e.trade_api_window, "交易接口限频统计窗口。", ["s", "m"]) +
      fieldWrap("engine.trade_api_max_calls", "窗口内最大调用", numberInput("engine.trade_api_max_calls", e.trade_api_max_calls, { type: "int", unit: "次", step: "1", min: 1 }), "限频窗口内允许的交易接口调用次数。") +
      durationField("engine.trade_api_min_gap", "调用最小间隔", e.trade_api_min_gap, "两次交易接口调用之间的最小间隔。", ["ms", "s"]) +
      durationField("engine.quote_request_timeout", "请求超时", e.quote_request_timeout, "单次行情 / 交易请求超时时间。", ["s", "m"]) +
      "</div></details>";
  }

  function renderServerCard(cfg) {
    const s = cfg.server;
    return '<details class="cfg-card"><summary>Web 服务 <span class="sub">地址与端口需重启生效</span></summary><div class="cfg-grid">' +
      textField("server.host", "监听地址", s.host, "常用 0.0.0.0（全部网卡）或 127.0.0.1（仅本机）。") +
      fieldWrap("server.port", "监听端口", numberInput("server.port", s.port, { type: "int", step: "1", min: 1, max: 65535 }), "") +
      fieldWrap("server.auth_secret", "面板密钥",
        '<div class="control"><input type="password" data-path="server.auth_secret" data-type="text" value="' + esc(s.auth_secret) + '" autocomplete="off" spellcheck="false">' +
        '<button type="button" class="icon-btn" data-action="toggle-secret">显示</button><button type="button" class="icon-btn" data-action="gen-secret">随机生成</button></div>',
        "留空不启用。启用后所有页面与接口都要求请求头 X-Admin-Secret 匹配，通常由反向代理注入。", { wide: true }) +
      "</div></details>";
  }

  function renderBody() {
    const body = drawer.querySelector("#cfg-body");
    if (state.loading) { body.innerHTML = '<div class="cfg-loading">正在读取配置…</div>'; return; }
    if (state.error) { body.innerHTML = '<div class="cfg-error">' + esc(state.error) + '</div><div class="cfg-add"><button type="button" class="btn" data-action="reload">重试</button></div>'; return; }
    const cfg = state.draft;
    const reloadError = (latestStatus || {}).config_reload_error;
    body.innerHTML = (reloadError ? '<div class="issue danger">引擎未能应用当前配置文件，仍按旧配置运行：' + esc(reloadError) + "</div>" : "") + renderModeCard(cfg) + renderStocksCard(cfg) + renderFeesCard(cfg) + renderEngineCard(cfg) + renderServerCard(cfg) +
      '<section class="cfg-card"><div class="cfg-card-head"><h3>检查结果</h3></div><div class="cfg-issues" data-issues></div></section>';
    refreshDerived();
  }

  function setHint(path, text, tone) {
    const el = drawer.querySelector('[data-hint="' + path + '"]');
    if (!el) return;
    el.innerHTML = text || "";
    el.className = "field-hint" + (tone ? " " + tone : "");
    const field = el.closest(".field");
    if (field) field.classList.toggle("invalid", tone === "danger" && !!text);
  }

  function refreshDerived() {
    if (!state.draft || state.loading || state.error) return;
    const cfg = state.draft;
    const result = analyze();
    const live = result.live;

    const modeCard = drawer.querySelector("[data-mode-card]");
    if (modeCard) modeCard.classList.toggle("live", live);
    drawer.querySelectorAll(".mode-option").forEach((btn) => btn.classList.toggle("active", (btn.dataset.value === "live") === live));
    const exposure = num(cfg.engine.max_exposure);
    drawer.querySelectorAll('[data-set-path="engine.max_exposure"]').forEach((chip) => chip.classList.toggle("active", num(chip.dataset.setValue) === exposure));
    const used = toNumber(((latestStatus || {}).exposure || {}).used);
    const full = totalFull(cfg);
    setHint("engine.max_exposure",
      (used !== null ? "当前已投入 " + money(used) + " · " : "") + "启用标的满仓合计约 " + money(full) +
      (exposure > 0 ? " · 上限 " + money(exposure) + (full > exposure ? "，满仓前会被上限截停" : "，足够覆盖全部满仓") : ""),
      exposure <= 0 ? (live ? "danger" : "warn") : (full > exposure ? "info" : "ok"));
    setHint("engine.dry_run", live ? "真实下单模式：策略触发时会真实成交，请确认资金上限与各标的参数。" : "演练模式：面板照常显示决策，但不会提交任何订单。", live ? "danger" : "ok");
    const poll = parseDuration(cfg.engine.poll_interval);
    setHint("engine.poll_interval", poll !== null && poll < 3000 ? "低于 3 秒容易触发接口限频" : "", "warn");

    drawer.querySelectorAll("[data-toggle-text]").forEach((el) => {
      const checked = !!getPath(cfg, el.dataset.toggleText);
      el.textContent = checked ? el.dataset.on : el.dataset.off;
    });

    const countEl = drawer.querySelector("[data-stock-count]");
    if (countEl) countEl.textContent = enabledStocks(cfg).length + " 启用 / " + cfg.stocks.length + " 总计";
    const origBySymbol = new Map((state.original.stocks || []).map((s) => [normalizeSymbol(s.symbol), s]));
    const remarkCount = new Map();
    enabledStocks(cfg).forEach((s) => { const r = String(s.remark || "").trim(); if (r) remarkCount.set(r, (remarkCount.get(r) || 0) + 1); });
    cfg.stocks.forEach((stock, i) => {
      const p = (key) => "stocks." + i + "." + key;
      const d = stockDerived(stock);
      const sym = normalizeSymbol(stock.symbol);
      const enabled = stock.enabled !== false;
      const card = drawer.querySelector('[data-stock-index="' + i + '"]');
      if (card) {
        card.classList.toggle("off", !enabled);
        card.classList.toggle("on", enabled && !(d.st.position_qty > 0));
        card.classList.toggle("has-position", enabled && d.st.position_qty > 0);
        card.querySelectorAll(".field").forEach((f) => { if (!f.dataset.field.endsWith(".enabled")) f.classList.toggle("disabled", !enabled); });
      }
      const facts = drawer.querySelector('[data-facts="' + i + '"]');
      if (facts) {
        facts.innerHTML = d.livePrice !== null
          ? "现价 <b>" + fmtNumber(d.livePrice) + "</b> · 持仓 <b>" + (d.st.position_qty || 0) + "</b> 股" + (d.heldLots ? "（约 " + d.heldLots + " 笔）" : "") +
            (d.st.pending_order_id ? ' · <span class="warn">有未完成挂单</span>' : "") + (d.st.cost_price ? " · 成本 " + fmtNumber(d.st.cost_price) : "")
          : (sym ? '<span class="muted">尚无行情数据，金额按建仓价估算</span>' : "");
      }
      const initial = num(stock.initial_price);
      if (d.livePrice !== null && initial > 0) {
        const diff = (d.livePrice - initial) / initial * 100;
        const below = d.livePrice <= initial;
        const risky = immediateBuy(stock, d, origBySymbol.get(sym));
        setHint(p("initial_price"), "现价 " + fmtNumber(d.livePrice) + "，" + (below ? "已低于建仓价 " : "高于建仓价 ") + pct(Math.abs(diff)) +
          (risky ? "。空仓时待反弹 " + pct(d.trail) + " 即买入一笔" : ""), risky ? (live ? "danger" : "warn") : "");
      } else setHint(p("initial_price"), initial > 0 ? "" : "必须大于 0", initial > 0 ? "" : "danger");
      const lot = state.lotSizes[sym] || 0;
      setHint(p("order_quantity"), (lot > 0 && d.qty % lot !== 0 ? "必须是每手 " + lot + " 股的整数倍。" : "") + "单笔约 " + money(d.perLot), lot > 0 && d.qty % lot !== 0 ? "danger" : "");
      setHint(p("max_lots"), "满仓约 " + money(d.full) + (d.heldLots ? " · 当前约 " + d.heldLots + " 笔" : ""), d.heldLots > d.lots ? "info" : "");
      setHint(p("sell_percent"), d.sell > 0 ? "买入价 × " + (1 + d.sell / 100).toFixed(4).replace(/0+$/, "") + " 进入卖出流程 · 单笔毛利约 " + fmtNumber(d.grossAtSell) + "，扣费 " + fmtNumber(d.fee) + " 后净利约 " + fmtNumber(d.netAtSell) +
        (d.grossAtSell > 0 && d.feeRatio >= 0.2 ? "，费用占毛利 " + pct(d.feeRatio * 100) : "") +
        (d.effectiveSell > d.sell + 0.0001 ? "，受最低盈利约束实际需涨约 " + pct(d.effectiveSell) : "") : "必须大于 0",
        d.sell <= 0 ? "danger" : d.feeRatio >= 0.5 ? "danger" : d.feeRatio >= 0.2 ? "warn" : d.effectiveSell > d.sell + 0.0001 ? "info" : "");
      setHint(p("trail_percent"), d.trail >= d.sell ? "必须小于卖出阈值 " + pct(d.sell) + "，否则可能亏损卖出" :
        d.trail > 0 ? "卖出至少锁定 +" + pct(d.locked) + "（卖点 " + pct(d.sell) + " 回撤 " + pct(d.trail) + "）· 买入需从低点反弹 " + pct(d.trail) : "必须大于 0", d.trail >= d.sell || d.trail <= 0 ? "danger" : "");
      setHint(p("buy_percent"), d.ladder.length
        ? '<span class="ladder">' + d.ladder.map((v, n) => "<span>第 " + (n + 1) + " 次补仓 −" + pct(v) + "</span>").join("") + "</span>"
        : (d.buy > 0 ? "最大笔数为 1 时不会补仓" : "必须大于 0"), d.buy <= 0 ? "danger" : d.buy < 1 ? "warn" : "");
      setHint(p("min_profit"), d.perLot > 0 ? (d.minProfit > d.netAtSell ? "高于到卖点的单笔净利 " + fmtNumber(d.netAtSell) + "，实际需涨约 " + pct(d.effectiveSell) : "按扣费后净利判断，到卖点约 " + fmtNumber(d.netAtSell) + "，满足要求") : "", d.minProfit > d.netAtSell && d.perLot > 0 ? "info" : "ok");
      const remark = String(stock.remark || "").trim();
      setHint(p("remark"), enabled && !remark ? "启用的标的必须填写" : enabled && remarkCount.get(remark) > 1 ? "与其他启用标的重复" : "", "danger");
      setHint(p("symbol"), "");
    });

    const sample = enabledStocks(cfg)[0];
    if (sample) {
      const sd = stockDerived(sample);
      setHint("fees", "示例：" + normalizeSymbol(sample.symbol) + " 每笔 " + sd.qty + " 股，买入约 " + fmtNumber(estimateFee(cfg.fees || {}, "buy", sd.price, sd.qty)) + "，卖出约 " + fmtNumber(estimateFee(cfg.fees || {}, "sell", sd.price, sd.qty)) + "，一买一卖合计约 " + fmtNumber(sd.fee), "info");
    }
    const feePresetKey = Object.keys(FEE_PRESETS).find((key) => Object.entries(FEE_PRESETS[key]).every(([k, v]) => num((cfg.fees || {})[k]) === num(v)));
    drawer.querySelectorAll('[data-action="fee-preset"]').forEach((chip) => chip.classList.toggle("active", chip.dataset.preset === feePresetKey));
    const issuesEl = drawer.querySelector("[data-issues]");
    if (issuesEl) {
      const items = result.errors.map((text) => '<div class="issue error">' + esc(text) + "</div>")
        .concat(result.issues.map((issue) => '<div class="issue ' + issue.level + '">' + esc(issue.text) + "</div>"));
      issuesEl.innerHTML = items.length ? items.join("") : '<div class="issue ok">未发现问题</div>';
    }
    const summary = drawer.querySelector("#cfg-summary");
    summary.innerHTML = "<span><b>" + result.changes.length + "</b> 项修改</span>" +
      (result.errors.length ? '<span class="danger">' + result.errors.length + " 个错误</span>" : "") +
      (result.dangers ? '<span class="danger">' + result.dangers + " 个高风险</span>" : "") +
      (result.warns ? '<span class="warn">' + result.warns + " 个提醒</span>" : "");
    const saveBtn = drawer.querySelector('[data-action="save"]');
    saveBtn.disabled = state.saving || result.errors.length > 0 || result.changes.length === 0;
    saveBtn.textContent = state.saving ? "正在应用…" : "保存并应用";
    saveBtn.classList.toggle("danger", result.dangers > 0 || result.switchingLive);
    saveBtn.classList.toggle("primary", !(result.dangers > 0 || result.switchingLive));
  }

  // ---------- events ----------
  function applyInput(target) {
    const path = target.dataset.path;
    if (!path) return;
    const type = target.dataset.type;
    if (type === "bool") setPath(state.draft, path, target.checked);
    else if (type === "int") setPath(state.draft, path, parseInt(target.value, 10) || 0);
    else if (type === "symbol") setPath(state.draft, path, target.value.toUpperCase());
    else if (type === "duration-n" || type === "duration-u") {
      const control = target.closest(".control");
      const n = control.querySelector('[data-type="duration-n"]').value;
      const unit = control.querySelector('[data-type="duration-u"]').value;
      setPath(state.draft, path, n === "" ? "" : String(Number(n)) + unit);
    } else setPath(state.draft, path, String(target.value).trim());
    const range = drawer.querySelector('[data-range-for="' + path + '"]');
    if (range && type !== "bool") range.value = num(target.value);
    refreshDerived();
  }

  function setValue(path, value) {
    setPath(state.draft, path, value);
    drawer.querySelectorAll('[data-path="' + path + '"]').forEach((el) => {
      if (el.dataset.type === "bool") el.checked = !!value;
      else if (el.dataset.type === "duration-n") el.value = durationParts(value).n;
      else if (el.dataset.type === "duration-u") el.value = durationParts(value).unit;
      else el.value = value;
    });
    const range = drawer.querySelector('[data-range-for="' + path + '"]');
    if (range) range.value = num(value);
    refreshDerived();
  }

  function onClick(event) {
    const btn = event.target.closest("[data-action]");
    if (!btn) return;
    const action = btn.dataset.action;
    if (action === "close" || action === "discard") { closeEditor(false); return; }
    if (action === "reload") { loadConfig(); return; }
    if (action === "save") { openConfirm(); return; }
    if (!state.draft) return;
    if (action === "set-mode") { setValue("engine.dry_run", btn.dataset.value !== "live"); return; }
    if (action === "chip") { setValue(btn.dataset.setPath, btn.dataset.setValue); return; }
    if (action === "fee-preset") {
      for (const [key, value] of Object.entries(FEE_PRESETS[btn.dataset.preset] || {})) setValue("fees." + key, value);
      return;
    }
    if (action === "step") {
      const input = drawer.querySelector('input[data-path="' + btn.dataset.path + '"]');
      const step = parseInt(input.dataset.stepSize, 10) || 1;
      const min = parseInt(input.dataset.min, 10) || 1;
      const current = parseInt(input.value, 10) || 0;
      let next = current + step * parseInt(btn.dataset.step, 10);
      if (step > 1) next = Math.round(next / step) * step;
      setValue(btn.dataset.path, Math.max(min, next));
      return;
    }
    if (action === "add-stock") {
      state.draft.stocks.push(clone(NEW_STOCK));
      renderBody();
      const cards = drawer.querySelectorAll(".stock-edit");
      const last = cards[cards.length - 1];
      if (last) { last.scrollIntoView({ behavior: "smooth", block: "center" }); const input = last.querySelector('[data-type="symbol"]'); if (input) input.focus(); }
      return;
    }
    if (action === "remove-stock") {
      const index = parseInt(btn.dataset.index, 10);
      const stock = state.draft.stocks[index];
      const st = statusFor(normalizeSymbol(stock.symbol)) || {};
      if (((st.position_qty || 0) > 0 || st.pending_order_id) && !window.confirm(normalizeSymbol(stock.symbol) + " 仍有" + heldText(st.position_qty || 0, st.pending_order_id) + "，移除后程序将不再管理它。确定移除？")) return;
      state.draft.stocks.splice(index, 1);
      renderBody();
      return;
    }
    if (action === "gen-remark") {
      const index = parseInt(btn.dataset.index, 10);
      setValue("stocks." + index + ".remark", autoRemark(state.draft.stocks[index].symbol));
      return;
    }
    if (action === "toggle-secret") {
      const input = drawer.querySelector('[data-path="server.auth_secret"]');
      input.type = input.type === "password" ? "text" : "password";
      btn.textContent = input.type === "password" ? "显示" : "隐藏";
      return;
    }
    if (action === "gen-secret") {
      if (!window.confirm("生成新密钥后，反向代理注入的 X-Admin-Secret 必须同步修改，否则保存后面板会立刻无法访问。继续？")) return;
      const bytes = new Uint8Array(32);
      crypto.getRandomValues(bytes);
      setValue("server.auth_secret", Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join(""));
    }
  }

  function onInput(event) {
    const target = event.target;
    if (target.dataset.rangeFor) {
      const path = target.dataset.rangeFor;
      setPath(state.draft, path, String(target.value));
      drawer.querySelectorAll('input[data-path="' + path + '"]').forEach((el) => { el.value = target.value; });
      refreshDerived();
      return;
    }
    if (target.dataset.path) applyInput(target);
  }

  function onBlur(event) {
    const target = event.target;
    if (target.dataset && target.dataset.type === "symbol" && state.draft) {
      const normalized = normalizeSymbol(target.value);
      if (normalized !== target.value) setValue(target.dataset.path, normalized);
      const index = parseInt(target.dataset.path.split(".")[1], 10);
      const stock = state.draft.stocks[index];
      if (stock && !String(stock.remark || "").trim() && normalized) setValue("stocks." + index + ".remark", autoRemark(normalized));
      if (stock && !state.lotSizes[normalized]) {
        const lot = 1;
        drawer.querySelectorAll('[data-path="stocks.' + index + '.order_quantity"]').forEach((el) => { if (el.dataset.stepSize) { el.dataset.stepSize = lot; el.dataset.min = lot; } });
      }
    }
  }

  // ---------- confirm & save ----------
  function openConfirm() {
    const result = analyze();
    if (result.errors.length || !result.changes.length) return;
    const needAck = result.dangers > 0 || result.switchingLive;
    const modal = document.createElement("div");
    modal.className = "cfg-modal-backdrop";
    modal.innerHTML = '<div class="cfg-modal" role="dialog" aria-modal="true"><header>确认应用 ' + result.changes.length + ' 项修改</header><div class="modal-body">' +
      '<div class="changes">' + result.changes.map((c) =>
        '<div class="change ' + esc(c.kind) + '"><span class="k">' + esc(c.label) + '</span><span class="v">' + (c.from !== "" ? '<span class="from">' + esc(c.from) + "</span>" : "") + '<span class="to">' + esc(c.to) + "</span></span></div>").join("") + "</div>" +
      (result.issues.length ? '<div class="modal-title-group">风险提示</div><div class="cfg-issues">' + result.issues.map((i) => '<div class="issue ' + i.level + '">' + esc(i.text) + "</div>").join("") + "</div>" : "") +
      "</div><footer>" +
      (needAck ? '<label class="ack"><input type="checkbox" data-ack> 我已了解上述高风险项' + (result.switchingLive ? "，确认开始真实下单" : "") + "</label>" : "<span></span>") +
      '<div class="cfg-foot-actions"><button type="button" class="btn" data-modal="cancel">返回修改</button><button type="button" class="btn ' + (needAck ? "danger" : "primary") + '" data-modal="confirm"' + (needAck ? " disabled" : "") + ">确认应用</button></div></footer></div>";
    document.body.appendChild(modal);
    const confirmBtn = modal.querySelector('[data-modal="confirm"]');
    const ack = modal.querySelector("[data-ack]");
    if (ack) ack.addEventListener("change", () => { confirmBtn.disabled = !ack.checked; });
    modal.addEventListener("click", (event) => {
      const btn = event.target.closest("[data-modal]");
      if (event.target === modal || (btn && btn.dataset.modal === "cancel")) { modal.remove(); return; }
      if (btn && btn.dataset.modal === "confirm") { modal.remove(); save(result.switchingLive); }
    });
  }

  function serializeDraft() {
    const cfg = clone(state.draft);
    cfg.server.port = parseInt(cfg.server.port, 10) || 0;
    cfg.engine.trade_api_max_calls = parseInt(cfg.engine.trade_api_max_calls, 10) || 0;
    cfg.engine.access_token_auto_refresh = cfg.engine.access_token_auto_refresh !== false;
    cfg.fees = Object.fromEntries(Object.entries(cfg.fees || {}).map(([k, v]) => [k, String(v)]));
    cfg.stocks = cfg.stocks.map((s) => ({
      symbol: normalizeSymbol(s.symbol), initial_price: String(s.initial_price), sell_percent: String(s.sell_percent), trail_percent: String(s.trail_percent),
      buy_percent: String(s.buy_percent), min_profit: String(s.min_profit), use_margin: !!s.use_margin,
      max_lots: parseInt(s.max_lots, 10) || 0, order_quantity: parseInt(s.order_quantity, 10) || 0, remark: String(s.remark || "").trim(), enabled: s.enabled !== false,
    }));
    return cfg;
  }

  async function waitForApply(prevReloadedAt, prevErrorAt) {
    const deadline = Date.now() + 90000;
    while (Date.now() < deadline) {
      await new Promise((r) => setTimeout(r, 1000));
      try {
        const resp = await fetch(apiURL("status"), { cache: "no-store" });
        if (!resp.ok) continue;
        const status = await resp.json();
        latestStatus = status;
        if (status.config_reload_error && (status.config_reload_error_at || "") !== prevErrorAt) return { error: status.config_reload_error };
        if (status.config_reloaded_at && status.config_reloaded_at !== prevReloadedAt) return { ok: true };
      } catch (_) {
        // 下一轮再试
      }
    }
    return { timeout: true };
  }

  async function save(confirmLive) {
    state.saving = true;
    refreshDerived();
    const before = latestStatus || {};
    try {
      const resp = await fetch(apiURL("config"), {
        method: "PUT", headers: { "Content-Type": "application/json" }, cache: "no-store",
        body: JSON.stringify({ config: serializeDraft(), confirm_live: !!confirmLive }),
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok || !data.ok) throw new Error(data.error || ("HTTP " + resp.status));
      const warnText = data.warnings && data.warnings.length ? "，警告：" + data.warnings.join("；") : "";
      if (!data.changed) {
        toast("配置没有变化", "");
        closeEditor(true);
        return;
      }
      toast("已写入配置文件，等待引擎应用…", "", 90000);
      const outcome = await waitForApply(before.config_reloaded_at || "", before.config_reload_error_at || "");
      state.saving = false;
      if (outcome.ok) {
        toast("配置已保存并应用" + warnText, "ok");
        closeEditor(true);
      } else if (outcome.error) {
        toast("配置已写入文件，但引擎拒绝应用，仍按旧配置运行：" + outcome.error, "danger", 12000);
        loadConfig();
      } else {
        toast("配置已写入文件，引擎尚未应用（可能正在轮询中），稍后可在面板底部查看热加载时间", "", 10000);
        closeEditor(true);
      }
      if (typeof pollStatus === "function") setTimeout(pollStatus, 500);
    } catch (err) {
      toast("保存失败：" + (err && err.message ? err.message : err), "danger", 8000);
    } finally {
      state.saving = false;
      if (state.open) refreshDerived();
    }
  }

  let toastTimer = null;
  function toast(text, tone, duration) {
    let el = document.getElementById("cfg-toast");
    if (!el) { el = document.createElement("div"); el.id = "cfg-toast"; document.body.appendChild(el); }
    el.className = "toast " + (tone || "");
    el.textContent = text;
    el.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => { el.hidden = true; }, duration || 4000);
  }

  // ---------- open / close / load ----------
  async function loadConfig() {
    state.loading = true; state.error = "";
    renderBody();
    try {
      const resp = await fetch(apiURL("config"), { cache: "no-store" });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) throw new Error(data.error || ("HTTP " + resp.status));
      state.path = data.path || "";
      state.lotSizes = data.lot_sizes || {};
      state.original = clone(data.config);
      state.draft = clone(data.config);
      drawer.querySelector("#cfg-path").textContent = state.path;
      state.loading = false;
      renderBody();
    } catch (err) {
      state.loading = false;
      state.error = "读取配置失败：" + (err && err.message ? err.message : err);
      renderBody();
    }
  }

  function ensureDrawer() {
    if (drawer) return;
    backdrop = document.createElement("div");
    backdrop.className = "cfg-backdrop";
    backdrop.hidden = true;
    backdrop.addEventListener("click", () => closeEditor(false));
    drawer = document.createElement("aside");
    drawer.className = "cfg-drawer";
    drawer.hidden = true;
    drawer.innerHTML =
      '<header class="cfg-head"><div><h2>配置</h2><code id="cfg-path"></code></div>' +
      '<div class="cfg-head-actions"><button type="button" class="btn ghost" data-action="reload">重新读取</button><button type="button" class="btn ghost" data-action="close">关闭</button></div></header>' +
      '<div class="cfg-body" id="cfg-body"></div>' +
      '<footer class="cfg-foot"><div class="cfg-summary" id="cfg-summary"></div>' +
      '<div class="cfg-foot-actions"><button type="button" class="btn" data-action="discard">放弃修改</button><button type="button" class="btn primary" data-action="save" disabled>保存并应用</button></div></footer>';
    drawer.addEventListener("click", onClick);
    drawer.addEventListener("input", onInput);
    drawer.addEventListener("change", (event) => { if (event.target.dataset && (event.target.dataset.type === "duration-u" || event.target.dataset.type === "bool")) applyInput(event.target); });
    drawer.addEventListener("focusout", onBlur);
    document.body.appendChild(backdrop);
    document.body.appendChild(drawer);
  }

  function openEditor() {
    ensureDrawer();
    state.open = true;
    backdrop.hidden = false;
    drawer.hidden = false;
    document.body.style.overflow = "hidden";
    loadConfig();
  }

  function closeEditor(force) {
    if (!drawer) return;
    if (!force && state.draft && state.original && JSON.stringify(state.draft) !== JSON.stringify(state.original) && !state.saving) {
      if (!window.confirm("有未保存的修改，确定放弃？")) return;
    }
    state.open = false;
    state.draft = null;
    state.saving = false;
    const saveBtn = drawer.querySelector('[data-action="save"]');
    saveBtn.textContent = "保存并应用";
    saveBtn.disabled = true;
    backdrop.hidden = true;
    drawer.hidden = true;
    document.body.style.overflow = "";
  }

  document.addEventListener("keydown", (event) => { if (event.key === "Escape" && state.open && !document.querySelector(".cfg-modal-backdrop")) closeEditor(false); });
  const openBtn = document.getElementById("open-config");
  if (openBtn) openBtn.addEventListener("click", openEditor);
  if (window.location.hash === "#config") openEditor();
})();
