# 通用服务镜像（Dockerfile 在各服务目录下保持一致）
#
# 构建入口：scripts/build-image.sh（由各服务 Taskfile 的 build-image 任务调用）
# 构建上下文：service/<name>/（需要 bin/ 下已有 scripts/build.sh 编译好的二进制）
#
# 特性：
#   - 非 root 运行：容器内用户 UID/GID 与宿主当前用户一致（构建参数 APP_UID/APP_GID）
#   - 数据与日志目录 /app/data、/app/logs 供运行时卷映射
#   - 配置默认复制进镜像；COPY_CONFIG=false 时不复制，改为运行时挂载
FROM alpine:3.22

# 容器内运行用户的 UID/GID（构建脚本传入宿主当前用户，保证挂载目录可写）
ARG APP_UID=1000
ARG APP_GID=1000
# 要复制进镜像的二进制文件名（如 server-linux-amd64，位于 bin/ 下）
ARG BINARY=server-linux-amd64
# 是否把 configs 目录复制进镜像；false 时依赖运行时挂载配置
ARG COPY_CONFIG=true

RUN addgroup -g "${APP_GID}" app \
    && adduser -D -u "${APP_UID}" -G app -s /sbin/nologin app

WORKDIR /app

# 二进制：由 scripts/build.sh 编译到 service/<name>/bin/ 后复制进来
COPY bin/${BINARY} /app/server

# 配置：COPY_CONFIG=true 时复制进镜像；false 时仅创建空目录供挂载
COPY configs/ /tmp/configs/
RUN mkdir -p /app/configs /app/data /app/logs \
    && if [ "${COPY_CONFIG}" = "true" ]; then cp -r /tmp/configs/. /app/configs/; fi \
    && rm -rf /tmp/configs \
    && chmod 0755 /app/server \
    && chown -R app:app /app

# 以普通用户运行
USER app

ENTRYPOINT ["/app/server"]
CMD ["-config", "/app/configs/config.yaml"]
