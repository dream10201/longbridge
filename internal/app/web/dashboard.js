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
      return date.toLocaleString("zh-CN", {
        hour12: false,
        timeZone: displayTimeZone
      });
    }

    function fmtShortDate(value) {
      if (!value) return "-";
      const date = new Date(value);
      if (Number.isNaN(date.getTime())) return esc(value);
      return date.toLocaleString("zh-CN", {
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        hour12: false,
        timeZone: displayTimeZone
      });
    }

    function fmtNumber(value, digits = 2) {
      if (value === null || value === undefined || value === "") return "-";
      const normalized = String(value).replace(/,/g, "").trim();
      if (!normalized) return "-";
      const num = Number(normalized);
      if (!Number.isFinite(num)) return esc(value);
      return num.toLocaleString("zh-CN", {
        minimumFractionDigits: digits,
        maximumFractionDigits: digits
      });
    }

    function toNumber(value) {
      if (value === null || value === undefined || value === "") return null;
      const num = Number(String(value).replace(/,/g, "").trim());
      return Number.isFinite(num) ? num : null;
    }

    function fmtSigned(value, digits = 2) {
      if (value === null || value === undefined || Number.isNaN(value)) return "-";
      const sign = value > 0 ? "+" : "";
      return sign + fmtNumber(value, digits);
    }

    function pctDistance(current, target) {
      if (current === null || target === null || target === 0) return null;
      return ((current - target) / target) * 100;
    }

    function compareTargets(a, b) {
      if (a === null || b === null) return 0;
      if (Math.abs(a - b) < 0.0000001) return 0;
      return a > b ? 1 : -1;
    }

    function resolveSellView(stock) {
      const base = toNumber(stock.base_sell_price || stock.next_sell_price);
      const trail = stock.sell_trail_armed ? toNumber(stock.sell_trail_trigger) : null;
      const current = toNumber(stock.price);
      const effective = trail !== null ? trail : base;
	      let statusText = "等待触发";
	      let driverText = "基础卖价";
      if (stock.sell_trail_armed) {
        if (compareTargets(trail, base) > 0) {
	          statusText = "追踪价已生效";
	          driverText = "追踪卖价";
	        } else {
	          statusText = "基础价仍生效";
	          driverText = "基础卖价";
	        }
      }
      return {
        base,
        trail,
        current,
        effective,
        statusText,
        driverText,
        delta: current !== null && effective !== null ? current - effective : null,
        deltaPct: pctDistance(current, effective),
      };
    }

    function resolveBuyView(stock) {
      const base = toNumber(stock.base_buy_price || stock.next_buy_price);
      const trail = stock.buy_trail_armed ? toNumber(stock.buy_trail_trigger) : null;
      const current = toNumber(stock.price);
      const effective = trail !== null ? trail : base;
	      let statusText = "等待触发";
	      let driverText = "基础买价";
	      if (stock.buy_trail_armed) {
	        if (compareTargets(trail, base) < 0) {
	          statusText = "追踪价已生效";
	          driverText = "追踪买价";
	        } else {
	          statusText = "基础价仍生效";
	          driverText = "基础买价";
	        }
      }
      return {
        base,
        trail,
        current,
        effective,
        statusText,
        driverText,
        delta: current !== null && effective !== null ? current - effective : null,
        deltaPct: pctDistance(current, effective),
      };
    }

    function distanceTone(delta, inverse = false) {
      if (delta === null) return "small";
      if (inverse) {
        return delta <= 0 ? "ok" : "warn";
      }
      return delta >= 0 ? "ok" : "warn";
    }

    let lastPollSuccessAt = null;
    let pollFailureCount = 0;
    let latestStatus = initialStatus;
    let stockFilter = "all";
    let previousStatus = null;
    let eventStream = [];

    function renderPollState(status) {
      const lastUpdated = status.last_updated_at ? new Date(status.last_updated_at) : null;
      const now = new Date();
      const marketOpen = !!((status.market || {}).open);
      // 休市时后端自适应轮询最长约 15 分钟才更新一次，放宽过期阈值，避免长期误报
      const staleThreshold = marketOpen ? 15000 : 16 * 60 * 1000;
      const stale = !lastUpdated || Number.isNaN(lastUpdated.getTime()) || (now - lastUpdated) > staleThreshold;
      const pollState = document.getElementById("poll-state");
      let text = "正常";
      let cls = stale ? "stale" : "live";
      if (pollFailureCount > 0) {
        text = "接口异常 " + pollFailureCount + " 次";
        cls = "warn";
      } else if (!marketOpen) {
        // 休市属正常状态，低频轮询，不报警
        text = "休市 / 低频轮询";
        cls = "small";
      } else if (stale) {
        text = "数据可能过期";
      } else if (lastPollSuccessAt) {
        text = "正常 / 最近成功 " + fmtDate(lastPollSuccessAt.toISOString());
      }
      pollState.textContent = text;
      pollState.className = cls;
    }

    function chipClass(kind, limit) {
      let base = "chip ";
      if (kind === "config") base += "chip-config";
      else if (kind === "funds") base += "chip-funds";
      else if (kind === "margin") base += "chip-margin";
      else base += "chip-neutral";
      if (limit) base += " limit";
      return base;
    }

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

	    function hasCancelText(value) {
	      const text = String(value || "");
	      return text.includes("撤单") || text.includes("撤销");
	    }

    function isCancelingOrder(stock) {
      return stock && stock.pending_order_id && (hasCancelText(stock.decision) || hasCancelText(stock.last_order_msg) || hasCancelText(stock.last_order_status));
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

    function marginSymbols(status) {
      return activeSymbols(status).filter((stock) => stock.use_margin);
    }

    function urgencyScore(stock, limited) {
      let score = 0;
      if (stock.last_error) score += 1000;
      if (stock.pending_order_id) score += 800;
      if (limited.has(stock.symbol)) score += 500;
      if (stock.buy_trail_armed || stock.sell_trail_armed) score += 300;
      const buyView = resolveBuyView(stock);
      const sellView = resolveSellView(stock);
      const distances = [buyView.deltaPct, sellView.deltaPct].filter((value) => value !== null).map((value) => Math.abs(value));
      if (distances.length) {
        const nearest = Math.min(...distances);
        if (nearest <= 0.5) score += 220;
        else if (nearest <= 1.5) score += 120;
        else if (nearest <= 3) score += 60;
      }
      return score;
    }

    function sortedActiveSymbols(status, limited) {
      return activeSymbols(status).slice().sort((a, b) => {
        const scoreDiff = urgencyScore(b, limited) - urgencyScore(a, limited);
        if (scoreDiff !== 0) return scoreDiff;
        return String(a.symbol).localeCompare(String(b.symbol));
      });
    }

    function nearestTriggerText(stock, buyView, sellView) {
      const candidates = [];
      if (buyView.effective !== null && buyView.deltaPct !== null) {
        candidates.push({ side: "买入", pct: Math.abs(buyView.deltaPct), delta: buyView.delta, value: buyView.effective, inverse: true });
      }
      if (sellView.effective !== null && sellView.deltaPct !== null) {
        candidates.push({ side: "卖出", pct: Math.abs(sellView.deltaPct), delta: sellView.delta, value: sellView.effective, inverse: false });
      }
      if (!candidates.length) return "无触发距离数据";
      candidates.sort((a, b) => a.pct - b.pct);
      const item = candidates[0];
      return "距离" + item.side + "触发 " + fmtSigned(item.delta) + " / " + fmtSigned(item.delta < 0 && !item.inverse ? -item.pct : item.pct) + "%，目标 " + fmtNumber(item.value);
    }

    function actionPreview(stock, limited) {
      const buyView = resolveBuyView(stock);
      const sellView = resolveSellView(stock);
      if (stock.last_error) return { text: "接口或订单异常，先处理错误", kind: "danger" };
      if (stock.pending_order_id) {
        return { text: "有" + actionText(stock.pending_side) + "挂单，等待成交或撤单确认", kind: "pending" };
      }
      if (limited.has(stock.symbol)) {
        return { text: "买入受限：" + (stock.buy_capacity_bottlenecks || []).join("、"), kind: "warn" };
      }
      if (stock.buy_trail_armed) {
        return { text: "追踪买入已激活，反弹到 " + fmtNumber(stock.buy_trail_trigger || buyView.effective) + " 将买入", kind: "buy" };
      }
      if (stock.sell_trail_armed) {
        return { text: "追踪止盈已激活，回撤到 " + fmtNumber(stock.sell_trail_trigger || sellView.effective) + " 将卖出", kind: "sell" };
      }
      return { text: nearestTriggerText(stock, buyView, sellView), kind: "neutral" };
    }

    function stockTimeline(stock) {
      const steps = [
        { label: "基础买价", active: !!stock.base_buy_price || !!stock.next_buy_price, done: stock.buy_trail_armed || stock.pending_side === "buy" },
        { label: "追踪买入", active: stock.buy_trail_armed, done: stock.pending_side === "buy" || stock.last_action === "buy" },
        { label: "基础卖价", active: !!stock.base_sell_price || !!stock.next_sell_price, done: stock.sell_trail_armed || stock.pending_side === "sell" },
        { label: "追踪止盈", active: stock.sell_trail_armed, done: stock.pending_side === "sell" || stock.last_action === "sell" },
        { label: "挂单/成交", active: !!stock.pending_order_id, done: !!stock.last_filled_at_text },
      ];
      return '<div class="timeline">' + steps.map((step) => {
        const cls = step.done ? "done" : step.active ? "active" : "";
        const icon = step.done ? "✓ " : step.active ? "● " : "";
        return '<span class="timeline-step ' + cls + '">' + icon + esc(step.label) + '</span>';
      }).join("") + '</div>';
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

    function renderOpsStrip(status) {
      const margins = marginSymbols(status);
      const token = status.access_token || {};
      const market = status.market || {};
      const tokenFailed = token.last_refresh_state === "error" || token.last_refresh_state === "missing";
      let tokenText, tokenClass;
      if (!token.has_token) {
        tokenText = "缺失"; tokenClass = "danger";
      } else if (tokenFailed) {
        tokenText = "刷新异常"; tokenClass = "danger";
      } else if (token.refresh_due_now) {
        tokenText = "刷新窗口内"; tokenClass = "warn";
      } else {
        tokenText = "已加载"; tokenClass = "ok";
      }
      const tokenSub = tokenFailed && token.last_refresh_message
        ? esc(token.last_refresh_message)
        : "到期 " + fmtDate(token.expires_at);
      const phase = marketPhaseText(market.current_phase);
      const marketState = market.trading_day ? "交易日" : "非交易日";
      const openClass = market.open ? "ok" : "small";
      const sessionRange = fmtShortDate(market.open_time) + " - " + fmtShortDate(market.close_time);
      const realOrderClass = status.dry_run ? "warn" : "danger";
      const configText = status.config_reloaded_at ? fmtDate(status.config_reloaded_at) : "启动后未热加载";
      const exposure = status.exposure || {};
      const usd = (status.account_balances || []).find((account) => account.currency === "USD") || {};
      const usdCash = (usd.cash_infos || [])[0] || {};
      let exposureValue = "未启用";
      let exposureClass = "warn";
      if (exposure.enabled) {
        exposureClass = exposure.reached ? "danger" : "ok";
        exposureValue = (exposure.used && exposure.limit)
          ? fmtNumber(exposure.used) + " / " + fmtNumber(exposure.limit)
          : (exposure.limit ? "上限 " + fmtNumber(exposure.limit) : "已启用");
      }
      const exposureSub = status.account_error
        ? esc(status.account_error)
        : "可用现金 " + fmtNumber(usdCash.available_cash) + " · 剩余融资 " + fmtNumber(usd.remaining_finance_amount) + " · 购买力 " + fmtNumber(usd.buy_power);
      document.getElementById("ops-strip").innerHTML =
        '<div class="ops-item risk">' +
          '<div class="ops-label">下单模式</div>' +
          '<div class="ops-value ' + realOrderClass + '">' + (status.dry_run ? '演练模式' : '真实下单') + '</div>' +
          '<div class="ops-sub">融资标的 ' + margins.length + ' 个</div>' +
        '</div>' +
        '<div class="ops-item"><div class="ops-label">美股市场</div><div class="ops-value ' + openClass + '">' + esc(phase) + ' / ' + marketState + '</div><div class="ops-sub">常规交易 ' + sessionRange + '</div></div>' +
        '<div class="ops-item"><div class="ops-label">已投入 / 最大投入</div><div class="ops-value ' + exposureClass + '">' + exposureValue + '</div><div class="ops-sub">' + exposureSub + '</div></div>' +
        '<div class="ops-item"><div class="ops-label">访问令牌</div><div class="ops-value ' + tokenClass + '">' + tokenText + '</div><div class="ops-sub">' + tokenSub + '</div></div>' +
        '<div class="ops-item"><div class="ops-label">配置</div><div class="ops-value">' + esc(configText) + '</div><div class="ops-sub"><code>' + esc(status.config_path || '-') + '</code></div></div>';
    }

    function renderPendingOrders(status) {
      const section = document.getElementById("pending-section");
      const pending = activeSymbols(status).filter((stock) => stock.pending_order_id);
      if (!pending.length) {
        section.innerHTML = "";
        return;
      }
      section.innerHTML =
        '<div class="pending-head">活跃挂单</div>' +
        '<div class="pending-grid">' +
          pending.map((stock) => {
            const sideClass = stock.pending_side === "sell" ? "sell" : "buy";
            const price = toNumber(stock.price);
            const pendingPrice = toNumber(stock.pending_price);
            const gap = price !== null && pendingPrice !== null ? price - pendingPrice : null;
            const gapPct = pctDistance(price, pendingPrice);
            return '<article class="pending-card ' + sideClass + '">' +
              '<div class="pending-title">' +
                '<strong>' + esc(stock.symbol) + '</strong>' +
                '<span class="status-chip active">' + esc(actionText(stock.pending_side)) + '</span>' +
              '</div>' +
              '<div class="pending-main">' + fmtNumber(stock.pending_price) + ' x ' + esc(stock.order_quantity || '-') + '</div>' +
              '<div class="pending-meta">当前价 ' + fmtNumber(stock.price) + ' / 价差 ' + fmtSigned(gap) + (gapPct === null ? '' : ' (' + fmtSigned(gapPct) + '%)') + '</div>' +
              '<div class="pending-meta">超时倒计时 ' + esc(stock.pending_timeout_left || '-') + '</div>' +
              '<code>' + esc(stock.pending_order_id) + '</code>' +
            '</article>';
          }).join("") +
        '</div>';
    }

    function renderEventStream(status) {
      const section = document.getElementById("event-section");
      if (!eventStream.length) {
        section.innerHTML = '<div class="label">最近事件</div><div class="small">暂无新的追踪、挂单或配置事件</div>';
        return;
      }
      section.innerHTML =
        '<div class="section-head"><div><div class="label">最近事件</div><div class="small">本页面打开后记录的关键变化</div></div></div>' +
        '<div class="event-list">' +
          eventStream.slice(0, 8).map((event) => {
            const icon = { danger: "✕", pending: "…", ok: "✓", info: "•" }[event.kind] || "•";
            return '<div class="event-item ' + esc(event.kind) + '">' +
              '<span class="event-time">' + esc(event.at) + '</span>' +
              '<span class="event-text">' + icon + " " + esc(event.text) + '</span>' +
            '</div>';
          }).join("") +
        '</div>';
    }


    function renderAccount(status) {
      const section = document.getElementById("account-section");
      if (status.account_error) {
        section.innerHTML = '<div class="label">账户资产</div><div class="danger">' + esc(status.account_error) + '</div>';
        return;
      }
      const accounts = status.account_balances || [];
      if (!accounts.length) {
        section.innerHTML = '<div class="label">账户资产</div><div class="small">暂无账户资产数据</div>';
        return;
      }
      section.innerHTML =
        '<div class="label">账户资产</div>' +
        '<table>' +
          '<thead><tr>' +
            '<th>币种</th><th>总现金</th><th>可用现金</th><th>可取现金</th><th>冻结现金</th><th>在途结算</th><th>最大融资</th><th>剩余融资</th><th>购买力</th><th>净资产</th><th>风险等级</th>' +
          '</tr></thead>' +
          '<tbody>' +
            accounts.map((account) => {
              const cash = (account.cash_infos || [])[0] || {};
              return '<tr>' +
                '<td data-label="币种"><strong>' + esc(account.currency || "-") + '</strong></td>' +
                '<td data-label="总现金">' + fmtNumber(account.total_cash) + '</td>' +
                '<td data-label="可用现金">' + fmtNumber(cash.available_cash) + '</td>' +
                '<td data-label="可取现金">' + fmtNumber(cash.withdraw_cash) + '</td>' +
                '<td data-label="冻结现金">' + fmtNumber(cash.frozen_cash) + '</td>' +
                '<td data-label="在途结算">' + fmtNumber(cash.settling_cash) + '</td>' +
                '<td data-label="最大融资">' + fmtNumber(account.max_finance_amount) + '</td>' +
                '<td data-label="剩余融资">' + fmtNumber(account.remaining_finance_amount) + '</td>' +
                '<td data-label="购买力">' + fmtNumber(account.buy_power) + '</td>' +
                '<td data-label="净资产">' + fmtNumber(account.net_assets) + '</td>' +
                '<td data-label="风险等级">' + esc(account.risk_level || "-") + '</td>' +
              '</tr>';
            }).join("") +
          '</tbody>' +
        '</table>';
    }

    function renderStockCard(stock, limited) {
      const buyView = resolveBuyView(stock);
      const sellView = resolveSellView(stock);
      const preview = actionPreview(stock, limited);
      const trend = stock.price_trend || "flat";
      const trendIcon = trend === "up" ? "▲" : trend === "down" ? "▼" : "•";
      const limitTags = (stock.buy_capacity_limit_tags || []).map((tag) => '<span class="' + chipClass(tag.kind, true) + '">' + esc(tag.label) + '</span>').join("");
      const detailTags = (stock.buy_capacity_detail_tags || []).map((tag) => '<span class="' + chipClass(tag.kind, false) + '">' + esc(tag.label) + '</span>').join("");
      const trendText = stock.price_change
        ? fmtNumber(stock.price_change) + (stock.price_change_pct ? " (" + fmtNumber(stock.price_change_pct) + "%)" : "")
        : "暂无涨跌数据";
      const buyMainValue = buyView.effective !== null ? buyView.effective : (stock.base_buy_price || stock.next_buy_price || "-");
      const buyStatusText = buyView.statusText;
      const buyStatusClass = stock.buy_trail_armed ? "active" : "wait";
      const buyTrailState = stock.buy_trail_armed
        ? "已激活" + (stock.buy_trail_percent ? " / 反弹 " + esc(stock.buy_trail_percent) + "%" : "")
        : "未激活" + (stock.buy_trail_percent ? " / 反弹 " + esc(stock.buy_trail_percent) + "%" : "");
      const sellMainValue = sellView.effective !== null ? sellView.effective : (stock.base_sell_price || stock.next_sell_price || "-");
      const sellStatusText = sellView.statusText;
      const sellStatusClass = stock.sell_trail_armed ? "active" : "wait";
	      const sellTrailState = stock.sell_trail_armed
	        ? "已激活" + (stock.sell_trail_percent ? " / 回撤 " + esc(stock.sell_trail_percent) + "%" : "")
	        : "未激活" + (stock.sell_trail_percent ? " / 回撤 " + esc(stock.sell_trail_percent) + "%" : "");
	      const sellProfit = stock.next_sell_profit || (stock.pending_side === "sell" ? stock.expected_profit : "");
	      const latestMeta =
	        '最近成交时间 ' + esc(stock.last_filled_at_text || '-') +
	        (stock.last_filled_price ? '<br>最近成交价 ' + fmtNumber(stock.last_filled_price) : '');
	      const statusMeta =
	        (sellProfit ? '预估毛利 ' + fmtNumber(sellProfit) + '<br>' : '') +
	        '最近订单 ' + esc(stock.last_order_status || '-') +
	        (stock.last_order_msg ? ' / ' + esc(stock.last_order_msg) : '') +
	        (stock.last_error ? '<br><span class="danger">' + esc(stock.last_error) + '</span>' : '');
      const pendingMeta = stock.pending_order_id
	        ? esc(actionText(stock.pending_side)) + ' @ ' + fmtNumber(stock.pending_price) + (stock.pending_timeout_left ? ' / 超时倒计时 ' + esc(stock.pending_timeout_left) : '')
	        : '当前无挂单';
	      const buyDistanceTone = distanceTone(buyView.delta, true);
	      const sellDistanceTone = distanceTone(sellView.delta, false);
      const marginText = stock.use_margin ? '允许融资' : '仅现金买入';
      return '' +
        '<article class="card stock-card priority-' + esc(preview.kind) + '">' +
          '<div class="stock-head">' +
            '<div>' +
              '<h2 class="stock-title">' + esc(stock.symbol) + '</h2>' +
              '<div class="stock-sub">最近动作 ' + esc(actionText(stock.last_action)) + ' / 最近买入价 ' + fmtNumber(stock.last_buy_price) + ' / 最近卖出价 ' + fmtNumber(stock.last_sell_price) + '</div>' +
            '</div>' +
            '<span class="badge ' + (stock.enabled ? '' : 'off') + '">' + (stock.enabled ? '启用中' : '已禁用') + '</span>' +
          '</div>' +
          '<div class="next-action ' + esc(preview.kind) + '">' +
            '<div class="next-action-label">下一动作预告</div>' +
            '<div class="next-action-text">' + esc(preview.text) + '</div>' +
          '</div>' +
          stockTimeline(stock) +
          '<div class="stock-overview">' +
            '<section class="price-hero">' +
              '<div class="label">最新价格</div>' +
              '<div class="stock-price ' + trend + '">' + fmtNumber(stock.price) + '</div>' +
              '<div class="trend ' + trend + '">' + trendIcon + ' ' + trendText + '</div>' +
              '<div class="stock-sub">' + latestMeta + '</div>' +
            '</section>' +
            '<section class="mini-grid">' +
              '<div class="mini-card">' +
                '<div class="mini-label">持仓</div>' +
                '<div class="mini-value">' + esc(stock.position_qty ?? 0) + ' 股</div>' +
                '<div class="mini-sub">可卖 ' + esc(stock.available_qty ?? 0) + ' 股</div>' +
              '</div>' +
              '<div class="mini-card">' +
                '<div class="mini-label">持仓成本</div>' +
                '<div class="mini-value">' + fmtNumber(stock.cost_price) + '</div>' +
                '<div class="mini-sub">最近动作 ' + esc(actionText(stock.last_action)) + '</div>' +
              '</div>' +
              '<div class="mini-card">' +
                '<div class="mini-label">当前持仓市值</div>' +
                '<div class="mini-value">' + fmtNumber(stock.position_notional) + '</div>' +
                '<div class="mini-sub">按当前价估算</div>' +
              '</div>' +
              '<div class="mini-card">' +
                '<div class="mini-label">满仓市值</div>' +
                '<div class="mini-value">' + fmtNumber(stock.max_position_notional) + '</div>' +
                '<div class="mini-sub">' + esc(stock.max_lots || 0) + ' 笔 x ' + esc(stock.order_quantity || 0) + ' 股</div>' +
              '</div>' +
            '</section>' +
          '</div>' +
          '<div class="exposure-row">' +
            '<span class="chip chip-funds">下一笔资金 ' + fmtNumber(stock.next_buy_cash_needed) + '</span>' +
            '<span class="chip ' + (stock.use_margin ? 'chip-margin' : 'chip-neutral') + '">' + esc(marginText) + '</span>' +
          '</div>' +
          '<div class="condition-grid">' +
            '<section class="condition-card buy">' +
              '<div class="condition-head">' +
                '<h3 class="condition-title">买入条件</h3>' +
                '<span class="status-chip ' + buyStatusClass + '">' + (stock.buy_trail_armed ? '● ' : '○ ') + buyStatusText + '</span>' +
              '</div>' +
              '<div class="condition-main">' + fmtNumber(buyMainValue) + '</div>' +
	              '<div class="condition-main-sub">当前触发价采用 ' + esc(buyView.driverText) + '</div>' +
              '<div class="condition-caption">' + esc(stock.next_buy_reason || '暂无买入条件') + '</div>' +
              '<div class="insight-grid">' +
	                '<div class="insight"><div class="insight-label">距离触发还差</div><div class="insight-value ' + buyDistanceTone + '">' + fmtSigned(buyView.delta) + '</div><div class="insight-sub">' + (buyView.deltaPct === null ? '-' : fmtSigned(buyView.deltaPct) + '%') + '</div></div>' +
	                '<div class="insight"><div class="insight-label">触发价来源</div><div class="insight-value">' + esc(buyView.driverText) + '</div><div class="insight-sub">' + esc(stock.buy_trail_armed ? '追踪买入已激活' : '追踪买入未激活') + '</div></div>' +
              '</div>' +
              '<div class="kv-list">' +
                '<div class="kv-row"><span class="kv-label">基础买价</span><span class="kv-value">' + fmtNumber(stock.base_buy_price || stock.next_buy_price) + '</span></div>' +
                '<div class="kv-row"><span class="kv-label">追踪买入</span><span class="kv-value">' + buyTrailState + '</span></div>' +
                '<div class="kv-row"><span class="kv-label">追踪最低价</span><span class="kv-value">' + fmtNumber(stock.buy_trail_lowest) + '</span></div>' +
                '<div class="kv-row"><span class="kv-label">当前追踪买价</span><span class="kv-value">' + (stock.buy_trail_armed ? fmtNumber(stock.buy_trail_trigger) : '未启动') + '</span></div>' +
                '<div class="kv-row"><span class="kv-label">补仓档位</span><span class="kv-value">' + esc(stock.next_buy_percent ? stock.next_buy_percent + "%" : "-") + '</span></div>' +
                '<div class="kv-row"><span class="kv-label">参考价</span><span class="kv-value">' + fmtNumber(stock.next_buy_ref) + '</span></div>' +
                '<div class="kv-row"><span class="kv-label">所需资金</span><span class="kv-value">' + fmtNumber(stock.next_buy_cash_needed) + '</span></div>' +
                '<div class="kv-row"><span class="kv-label">融资占用</span><span class="kv-value">' + fmtNumber(stock.next_buy_margin_use) + '</span></div>' +
                '<div class="kv-row"><span class="kv-label">还能买</span><span class="kv-value">' + esc(stock.remaining_buy_lots ?? 0) + ' 笔</span></div>' +
              '</div>' +
              (stock.buy_capacity_reason ? '<div class="panel-meta" style="margin-top:10px;">' + esc(stock.buy_capacity_reason) + '</div>' : '') +
              (limitTags ? '<div class="chips">' + limitTags + '</div>' : '') +
              (detailTags ? '<div class="chips">' + detailTags + '</div>' : '') +
            '</section>' +
            '<section class="condition-card sell">' +
              '<div class="condition-head">' +
                '<h3 class="condition-title">卖出条件</h3>' +
                '<span class="status-chip ' + sellStatusClass + '">' + (stock.sell_trail_armed ? '● ' : '○ ') + sellStatusText + '</span>' +
              '</div>' +
              '<div class="condition-main">' + fmtNumber(sellMainValue) + '</div>' +
	              '<div class="condition-main-sub">当前触发价采用 ' + esc(sellView.driverText) + '</div>' +
              '<div class="condition-caption">' + esc(stock.next_sell_reason || '暂无卖出条件') + '</div>' +
              '<div class="insight-grid">' +
	                '<div class="insight"><div class="insight-label">距离触发还差</div><div class="insight-value ' + sellDistanceTone + '">' + fmtSigned(sellView.delta) + '</div><div class="insight-sub">' + (sellView.deltaPct === null ? '-' : fmtSigned(sellView.deltaPct) + '%') + '</div></div>' +
	                '<div class="insight"><div class="insight-label">触发价来源</div><div class="insight-value">' + esc(sellView.driverText) + '</div><div class="insight-sub">' + esc(stock.sell_trail_armed ? '追踪止盈已激活' : '追踪止盈未激活') + '</div></div>' +
              '</div>' +
              '<div class="kv-list">' +
                '<div class="kv-row"><span class="kv-label">基础卖价</span><span class="kv-value">' + fmtNumber(stock.base_sell_price || stock.next_sell_price) + '</span></div>' +
                '<div class="kv-row"><span class="kv-label">追踪止盈</span><span class="kv-value">' + sellTrailState + '</span></div>' +
                '<div class="kv-row"><span class="kv-label">追踪最高价</span><span class="kv-value">' + fmtNumber(stock.sell_trail_highest) + '</span></div>' +
                '<div class="kv-row"><span class="kv-label">当前追踪卖价</span><span class="kv-value">' + (stock.sell_trail_armed ? fmtNumber(stock.sell_trail_trigger) : '未启动') + '</span></div>' +
                '<div class="kv-row"><span class="kv-label">参考价</span><span class="kv-value">' + fmtNumber(stock.next_sell_ref) + '</span></div>' +
                '<div class="kv-row"><span class="kv-label">预估毛利</span><span class="kv-value">' + fmtNumber(stock.next_sell_profit) + '</span></div>' +
              '</div>' +
            '</section>' +
          '</div>' +
          '<div class="stock-sections">' +
            '<section class="panel full">' +
              '<h3 class="panel-title">结构化状态</h3>' +
              '<div class="decision-summary">' +
                '<div class="insight"><div class="insight-label">策略触发价</div><div class="insight-value">' + fmtNumber(stock.trigger_price || sellMainValue || buyMainValue) + '</div><div class="insight-sub">本轮策略判定对应的目标价</div></div>' +
	                '<div class="insight"><div class="insight-label">预估毛利</div><div class="insight-value">' + fmtNumber(sellProfit) + '</div><div class="insight-sub">按当前卖出计划估算</div></div>' +
                '<div class="insight"><div class="insight-label">最近订单状态</div><div class="insight-value">' + esc(stock.last_order_status || '-') + '</div><div class="insight-sub">' + esc(stock.last_order_msg || '无附加信息') + '</div></div>' +
              '</div>' +
              '<details class="decision-details" data-symbol="' + esc(stock.symbol) + '"><summary>查看策略原始说明</summary><div class="state-text">' + esc(stock.decision || "-") + '</div></details>' +
              '<div class="panel-meta">' + statusMeta + '</div>' +
            '</section>' +
            '<section class="panel">' +
              '<h3 class="panel-title">挂单</h3>' +
              '<div class="panel-main">' + (stock.pending_order_id ? '<code>' + esc(stock.pending_order_id) + '</code>' : '-') + '</div>' +
              '<div class="panel-meta">' + pendingMeta + '</div>' +
            '</section>' +
            '<section class="panel">' +
              '<h3 class="panel-title">备注</h3>' +
              '<div class="state-text">' + esc(stock.remark || "-") + '</div>' +
            '</section>' +
          '</div>' +
        '</article>';
    }

    function renderStocks(status) {
      const grid = document.getElementById("stock-grid");
      // 重渲染前记录已展开的"策略原始说明"，渲染后恢复，避免每轮自动折叠
      const openDetails = new Set(
        Array.from(grid.querySelectorAll("details.decision-details[open]"))
          .map((el) => el.dataset.symbol)
          .filter(Boolean)
      );
      const limited = limitedSymbols(status);
      const stocks = sortedActiveSymbols(status, limited).filter((stock) => stockMatchesFilter(stock, limited));
      grid.innerHTML = stocks.length
        ? stocks.map((stock) => renderStockCard(stock, limited)).join("")
        : '<div class="empty-state">当前筛选条件下暂无标的</div>';
      if (openDetails.size) {
        grid.querySelectorAll("details.decision-details").forEach((el) => {
          if (openDetails.has(el.dataset.symbol)) el.open = true;
        });
      }
    }

    function renderDisabledStocks(status) {
      const disabled = disabledSymbols(status);
      const section = document.getElementById("disabled-section");
      if (!section) return;
      if (!disabled.length) {
        section.innerHTML = "";
        return;
      }
      section.innerHTML =
        '<div class="disabled-head">已禁用标的</div>' +
        '<div class="disabled-list">' +
          disabled.map((stock) =>
            '<div class="disabled-item">' +
              '<strong>' + esc(stock.symbol) + '</strong>' +
              (stock.remark ? '<span>' + esc(stock.remark) + '</span>' : '') +
            '</div>'
          ).join("") +
        '</div>';
    }

    function renderStatus(status) {
      recordStatusEvents(status);
	      document.getElementById("last-updated").textContent = fmtDate(status.last_updated_at);
	      renderPollState(status);
	      renderOpsStrip(status);
      renderPendingOrders(status);
      renderEventStream(status);
      updateToolbar(status);
      renderAccount(status);
      renderStocks(status);
      renderDisabledStocks(status);
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
        const resp = await fetch(buildStatusURL(), { cache: "no-store" });
        if (!resp.ok) {
          pollFailureCount += 1;
          renderPollState(latestStatus);
          return;
        }
        const status = await resp.json();
        lastPollSuccessAt = new Date();
        pollFailureCount = 0;
        latestStatus = status;
        renderStatus(status);
      } catch (_) {
        pollFailureCount += 1;
        renderPollState(latestStatus);
      }
    }

    function nextPollDelay() {
      // 与后端自适应轮询对齐：休市时退避，避免空耗请求拉取相同快照
      return ((latestStatus || {}).market || {}).open ? openPollIntervalMs : closedPollIntervalMs;
    }

    function scheduleNextPoll() {
      setTimeout(async () => {
        await pollStatus();
        scheduleNextPoll();
      }, nextPollDelay());
    }

    bindToolbar();
    lastPollSuccessAt = new Date();
    renderStatus(initialStatus);
    scheduleNextPoll();
