# ---- Go 构建 ----
# 基础镜像走 ECR Public 的 Docker Hub 官方镜像副本,避开 auth.docker.io 故障/限流
FROM public.ecr.aws/docker/library/golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/longbridge .

# ---- 运行镜像 ----
# distroless/static:只有 CA 证书 + 时区 + 二进制,无 shell/包管理器。
# 时区数据同时已内嵌进二进制(time/tzdata),不依赖基础镜像。
# 注意:无 shell 意味着 docker exec 进不去,排障靠 docker logs。
FROM gcr.io/distroless/static-debian12
COPY --from=build /out/longbridge /usr/local/bin/longbridge

# config.toml 里的相对路径(state.json/.env)以配置文件所在目录为基准,都落在 /data
WORKDIR /data
VOLUME ["/data"]

# 20017: Web 监控面板
EXPOSE 20017

# distroless 无 curl,健康检查用二进制自带的 -healthcheck 子命令(必须 exec 形式)
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=5 \
    CMD ["/usr/local/bin/longbridge", "-healthcheck"]

ENTRYPOINT ["/usr/local/bin/longbridge"]
CMD ["-config", "/data/config.toml"]
