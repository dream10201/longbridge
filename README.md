# longbridge

基于长桥证券（LongPort）OpenAPI 的美股**网格化波段交易脚本**，使用 Go 编写。带本地状态持久化和 Web 状态面板，支持「追踪确认」式的两段式买卖、按持仓深度递增的补仓阶梯、多重风控和 Access Token 自动刷新。

> ⚠️ 这是真实下单工具。`config.toml` 的 `dry_run` 决定是否真正提交订单——首次使用务必先设为 `true` 验证策略判断，确认无误后再切到 `false`。

## 目录结构

```
main.go                 启动入口：加载配置 → 初始化引擎 → 起 HTTP 服务 + 交易循环
config.toml             运行配置（监听地址、引擎参数、多只股票）
state.json              本地状态持久化（买卖锚点、追踪状态、挂单）
.env                    LongPort 凭证（App Key / Secret / Access Token）
build.sh                构建脚本
internal/app/
├── config.go           配置解析与校验
├── auth.go             Access Token 临期自动刷新，并写回 .env
├── longport.go         LongPort SDK 客户端封装（含交易接口限频）
├── proxy.go            代理感知的 HTTP 客户端
├── strategy.go         ★ 核心策略：纯函数，输入快照 → 输出决策
├── state.go            state.json 读写（原子替换）
├── engine.go           引擎主循环编排
├── engine_market.go    交易时段 / 市场时钟
├── engine_quotes.go    行情拉取
├── engine_orders.go    下单 / 撤单 / 挂单同步 / 券商挂单恢复
├── engine_portfolio.go 持仓聚合 / 资金 / 买入容量估算
├── engine_state.go     追踪买卖状态机更新
└── web.go              Web 面板 HTTP handler
```

## 运行

1. 在 `config.toml` 同目录准备 `.env`（包含 LongPort App Key / Secret / Access Token）
2. 检查 `config.toml`，**首次保持 `dry_run = true`**
3. 启动：

```bash
go run .
# 或
./build.sh && ./longbridge
```


### Docker 部署(推荐)

```bash
mkdir -p data
cp config.toml data/config.toml   # server.host 保持 0.0.0.0
cp .env data/.env                 # LongPort 凭据;token 自动刷新会写回此文件
docker compose up -d --build
```

- 面板: http://127.0.0.1:20017 (compose 只绑定宿主机本机)
- `state.json` 自动落在 `data/` 下;`config.toml` 里的相对路径以配置文件所在目录为基准
- 运行镜像是 `gcr.io/distroless/static`(约 12MB,无 shell),排障用 `docker logs longbridge`;
  构建机拉不动 gcr.io 时,把 Dockerfile 运行段换回 `public.ecr.aws/docker/library/debian:bookworm-slim`
- GitHub Actions 会在 push 时构建多架构镜像推到 `ghcr.io/<owner>/longbridge`(私有仓库需先 `docker login ghcr.io`):
  `docker pull ghcr.io/dream10201/longbridge:latest`

4. 打开监控面板（地址由 `[server]` 决定）：

```text
http://127.0.0.1:20017/
```

面板展示：市场时段、各股持仓 / 成本 / 现价、下一个买卖触发价、追踪状态、挂单与超时倒计时、最近成交、账户资金、现金上限使用情况、Access Token 刷新状态。`/api/status` 返回同样数据的 JSON。

## 策略说明

策略是一个**带追踪确认的网格补仓策略**，买卖都分两步，避免在单点直接成交：

### 买入（两步）

1. **基础买点**
   - 空仓且无最近卖出锚点：现价 ≤ `initial_price` 即激活
   - 空仓且上一动作是卖出：参考最近卖出价 `last_sell * (1 - buy_percent)`
   - 已持仓补仓：参考最近买入价 `last_buy * (1 - buy_percent)`
2. **追踪确认**：到达基础买点后记录最低价，价格从低点反弹 `trail_percent` 时才买入一笔（避免接飞刀）

补仓阈值按 **Fibonacci 风格随持仓深度放大**（`base × fib(n+2)/2`）：

| 当前持仓笔数 | 1 | 2 | 3 | 4 |
|---|---|---|---|---|
| 下一档买入阈值（`buy_percent=2`） | ~2% | ~3% | ~5% | ~8% |

卖出一笔后，下一档阈值随持仓笔数自动回落（例如 5% → 3%）。

### 卖出（两步）

1. **基础卖点**：现价 ≥ `last_buy * (1 + sell_percent)`，且单笔预估毛利 ≥ `min_profit`，激活追踪止盈
2. **追踪确认**：记录最高价，价格从高点回撤 `trail_percent` 时才卖出一笔（让利润奔跑）

### 其它规则

- 卖出后把卖出价压入「卖出锚点栈」，后续买回优先参考最近一次卖出价
- 已有未完成挂单时不重复下单；挂单超过 `order_timeout` 自动撤单
- 启动 / 每轮会用券商侧实际持仓校准本地买入阶梯（`remark` 用于识别本程序的挂单）
- 策略关键配置变化时，会重置追踪状态并撤销旧挂单

## 配置说明

### `[server]`

| 字段 | 说明 | 默认 |
|---|---|---|
| `host` / `port` | Web 面板监听地址 | `0.0.0.0` / 必填 |
| `auth_secret` | 访问校验密钥；留空不启用 | 空 |

- 配置 `auth_secret` 后，所有页面和接口（`/healthz` 除外）都要求请求携带 `X-Admin-Secret: <auth_secret>`，否则返回 401
- 典型用法是放在反向代理后面由代理注入,例如 Caddy：`header_up X-Admin-Secret your-secret`
- Docker 健康检查走免鉴权的 `/healthz`，不受影响

### `[engine]`

| 字段 | 说明 | 默认 |
|---|---|---|
| `poll_interval` | 轮询周期（拉行情 / 持仓 / 订单） | `5s` |
| `order_timeout` | 挂单超时自动撤单 | `90s` |
| `state_file` | 状态持久化文件；运行中不热切换 | `state.json` |
| `dry_run` | `true` 只演练不下单 | `false` |
| `cash_limit` | 全局现金使用上限；`0` 关闭 | `0` |
| `cash_limit_mode` | `used` / `projected`（见下） | `used` |
| `access_token_auto_refresh` | 临期自动刷新 token | `true` |
| `access_token_refresh_before` | 提前多久刷新 | `1h` |
| `access_token_env_file` | 新 token 写回的 `.env` 路径 | `.env` |
| `trade_api_window` / `trade_api_max_calls` / `trade_api_min_gap` | 交易接口限频 | `30s` / `30` / `25ms` |
| `quote_request_timeout` | 单次请求超时 | `15s` |

- `cash_limit_mode = "used"`：当前已用现金 ≥ `cash_limit` 时停止买入
- `cash_limit_mode = "projected"`：本次买入后已用现金会超过 `cash_limit` 时也提前停止
- 「已用现金」按本程序启用股票的当前持仓名义 + 待成交买单名义估算（strategy exposure）

### `[[stocks]]`（每只股票一个）

| 字段 | 说明 |
|---|---|
| `symbol` | 美股代码，必须以 `.US` 结尾 |
| `initial_price` | 空仓首次建仓的基础买点 |
| `sell_percent` | 基础卖点涨幅百分比（`9.0` = 9%） |
| `trail_percent` | 追踪阈值百分比：卖出时为从最高价回撤、买入时为从最低价反弹；默认 `1.0` |
| `buy_percent` | 基础补仓阈值百分比，会按持仓笔数放大 |
| `min_profit` | 单笔卖出预估毛利下限，未达到不卖 |
| `use_margin` / `max_margin` | 是否允许融资；`max_margin` 限制该股最大名义持仓（仅 `use_margin=true` 生效） |
| `max_lots` | 单只最多持有笔数；总持仓 = `max_lots × order_quantity` |
| `order_quantity` | 每笔下单数量；须为券商 lot size 整数倍 |
| `remark` | 启用股票必须配置且全局唯一，用于从券商侧恢复本程序挂单 |
| `enabled` | 是否启用 |

## 风控与健壮性

- **多重买入约束**：`max_lots`（笔数）、`cash_limit`（全局现金）、`max_margin`（融资名义）、券商 `EstimateMaxPurchaseQuantity`（实际购买力）、`min_profit`（卖出利润下限）
- **挂单超时撤单**、**重复下单保护**、**券商侧挂单恢复**
- **崩溃自愈**：单轮 panic 被 `recover` 捕获并记录，会话失效 / token 刷新后自动重建 LongPort 客户端
- **自适应轮询**：盘外、临近开盘、有挂单、token 临期时动态调整轮询间隔，节省 API 调用
- **配置热加载**：检测 `config.toml` 修改时间变化即重载（监听地址、状态文件除外）
- **状态原子写入**：`state.json` 通过临时文件 + rename 替换

> 注意：这是网格补仓策略，**没有止损**。单边下跌中会持续补仓到 `max_lots` 后套牢；风险由 `max_lots`、`cash_limit`、`max_margin` 共同约束，请据此设置仓位上限。

## 注意事项

- LongPort Go 模块实际导入路径是 `github.com/longbridge/openapi-go`
- 真实下单前确认 `.env` 指向模拟盘或目标账户
- Access Token 刷新走官方 Refresh 接口，刷新成功后旧 token 失效，程序会自动重建客户端并把新 token 写回 `.env`
- 下单价格统一按 2 位小数处理（买单向上进位、卖单向下取整守住触发价）
